#!/usr/bin/env python3
"""Opt-in finite Pi/Codex exchange in owned Herdr panes; may consume quota.

No persistent notifier is installed. The test driver uses Codex's native queue
for one return notice; Pi uses the process-owned RPC extension. Native histories
use the installed harness configuration, as in real-harness-lifecycle.sh.
"""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile
import time

REPO = Path(__file__).resolve().parents[2]


def main():
    if os.environ.get('KANBI_REAL_HARNESS_TESTS') != '1':
        print('Opt in: KANBI_REAL_HARNESS_TESTS=1 python3 scripts/prototypes/real-session-messaging.py')
        return
    root = Path(tempfile.mkdtemp(prefix='kanbi-real-messages-'))
    runtime = root / 'runtime'
    fixture = REPO / 'scripts/herdr-fixture.py'
    spec = importlib.util.spec_from_file_location('message_fixture', fixture)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    base_env = {k: v for k, v in os.environ.items() if not k.startswith('HERDR_')}
    base_env.update(KANBI_CONFIG=str(root / 'config.yaml'), KANBI_DB=str(root / 'kanbi.db'),
                    KANBI_STATE_DIR=str(root / 'state'), KANBI_DATA_DIR=str(root / 'data'),
                    GOCACHE=os.environ.get('GOCACHE', '/tmp/kanbi-go-build'))
    binary = str(root / 'kanbi')
    subprocess.run(['go', 'build', '-o', binary, './cmd/kanbi'], cwd=REPO, env=base_env, check=True)
    # JSON arrays and strings are YAML-compatible. All paths belong to this fixture.
    pi_command = ['env', 'KANBI_MAILBOX_CLI=' + binary, os.path.realpath(shutil.which('pi')), '--mode', 'rpc',
                  '--no-extensions', '-e', str(REPO / 'scripts/prototypes/pi-mailbox.mjs')]
    codex_command = ['codex', '--no-alt-screen', '--ask-for-approval', 'never',
                     '--sandbox', 'workspace-write']
    (root / 'config.yaml').write_text(
        'multiplexer:\n  default: herdr\nprompt_ready_timeout: 60s\nharnesses:\n'
        + '  pi:\n    start: ' + json.dumps(pi_command) + '\n'
        + '  codex:\n    start: ' + json.dumps(codex_command) + '\n')
    db = None
    success = False
    try:
        subprocess.run(['python3', str(fixture), 'start', str(runtime), '160', '45'],
                       env=base_env, check=True)
        env = dict(base_env)
        # The helper supplies only this disposable server's socket and XDG paths.
        env.update({k: v for k, v in helper.environment(runtime).items()
                    if k.startswith(('HERDR_', 'XDG_'))})

        def command(*args, timeout=90):
            result = subprocess.run(args, env=env, cwd=root, text=True,
                                    capture_output=True, timeout=timeout)
            if result.returncode:
                raise RuntimeError(f'{args[0]} failed: {result.stderr[-2000:]} {result.stdout[-2000:]}')
            return result.stdout

        def kanbi(*args):
            return command(binary, *args)

        def wait_for(label, probe, seconds=150):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                value = probe()
                if value:
                    return value
                time.sleep(.5)
            raise RuntimeError('timed out: ' + label)

        # Add explicitly on the fixture cwd, avoiding any user's board/database.
        kanbi('boards', 'set-cwd', 'Default', str(root))
        kanbi('add', 'Native Pi recipient', '--harness', 'pi')
        kanbi('open', 'T-001')
        db = sqlite3.connect(root / 'kanbi.db')
        pi_id = db.execute("select id from sessions where harness='pi'").fetchone()[0]
        prompt = (
            'This is an authorized disposable Kanbi messaging acceptance test. '
            'Use only the Kanbi mailbox; do not edit project files. '
            f'Your CLI is {binary}. Sender identity and database are in KANBI_SESSION_ID and KANBI_DB. '
            f'Send to session {pi_id} with request key real-schema-ready and body '
            f'"Read this message, acknowledge its ID with {binary} messages ack, then reply '
            f'with request key real-pi-reply and body KANBI_PI_REPLY_OK using {binary} messages reply. '
            'Do not send further replies." '
            'Run the send command yourself with your shell tool. Then finish this turn with '
            'KANBI_SENDER_WAITING. Do not poll or wait for a response in this turn.'
        )
        kanbi('add', 'Native Codex sender', '--harness', 'codex', '--body', prompt)
        kanbi('open', 'T-002', '--send-prompt')
        codex_id, codex_pane = db.execute(
            "select id,json_extract(mux_metadata,'$.pane_id') from sessions where harness='codex'"
        ).fetchone()
        reply_id = wait_for('Pi read, acknowledgement and reply', lambda: db.execute(
            "select id from session_messages where sender=? and recipient=? and body='KANBI_PI_REPLY_OK'",
            (pi_id, codex_id)).fetchone())[0]
        assert db.execute('select state from session_messages where sender=?',
                          (codex_id,)).fetchone()[0] == 'acknowledged'
        command('herdr', 'agent', 'wait', codex_pane, '--until', 'idle', '--until', 'done',
                '--until', 'blocked', '--timeout', '30000')
        codex_ref = wait_for('verified native Codex ref', lambda: db.execute(
            'select harness_session_ref from sessions where id=?', (codex_id,)).fetchone()[0])
        # One native queue notice is the finite prototype notifier, not a daemon.
        notice = (f'Kanbi reply {reply_id} pending for your exact session {codex_id}. '
                  f'Run {binary} messages inbox, read and acknowledge the reply ID with '
                  f'{binary} messages ack ID. Do not reply to this receipt. '
                  'Finish with exactly KANBI_MESSAGE_REPLY_OK.')
        command('codex', 'queue', '--thread', codex_ref, '--message', notice)
        command('herdr', 'pane', 'wait-output', codex_pane, '--regex',
                r'(?m)^\s*(?:•\s*)?KANBI_MESSAGE_REPLY_OK\s*$', '--source', 'recent',
                '--timeout', '120000', timeout=130)
        wait_for('Codex acknowledged reply', lambda: db.execute(
            "select id from session_messages where sender=? and recipient=? and state='acknowledged'",
            (pi_id, codex_id)).fetchone())
        # Type a draft only into the owned Codex pane. Queue notification must
        # preserve it; it may run immediately or defer until the draft is cleared.
        draft = 'KANBI_UNSENT_DRAFT_396'
        command('herdr', 'pane', 'send-text', codex_pane, draft)
        command('codex', 'queue', '--thread', codex_ref, '--message',
                'Native queue draft-safety probe. Reply exactly KANBI_DRAFT_QUEUE_OK. '
                'Do not run tools or modify any draft text.')
        draft_probe = subprocess.run(
            ['herdr', 'pane', 'wait-output', codex_pane, '--regex',
             r'(?m)^\s*(?:•\s*)?KANBI_DRAFT_QUEUE_OK\s*$', '--source', 'recent',
             '--timeout', '20000'], env=env, cwd=root, capture_output=True, text=True,
            timeout=30)
        screen = command('herdr', 'pane', 'read', codex_pane, '--source', 'visible', '--format', 'text')
        (root / 'draft-screen.txt').write_text(screen)
        assert draft in screen, 'native queue lost unsent draft'
        if draft_probe.returncode == 0:
            assert screen.rfind(draft) > screen.rfind('KANBI_DRAFT_QUEUE_OK'), 'draft became a submitted turn'
        command('herdr', 'pane', 'send-keys', codex_pane, *(['backspace'] * len(draft)))
        if draft_probe.returncode:
            command('herdr', 'pane', 'wait-output', codex_pane, '--regex',
                    r'(?m)^\s*(?:•\s*)?KANBI_DRAFT_QUEUE_OK\s*$', '--source', 'recent',
                    '--timeout', '120000', timeout=130)
        print('PASS: native Codex queue preserved the owned unsent draft')

        # A tool-created marker proves that a real tool call is still in flight.
        # Queue the notice during sleep and require the original call to finish.
        start = root / 'busy-started'
        finished = root / 'busy-finished'
        busy_prompt = (f'Acceptance probe in this disposable directory: use your shell tool '
                       f'to execute touch {start}; sleep 8; touch {finished}. '
                       'Let that command finish, then say KANBI_BUSY_ORIGINAL_OK.')
        command('herdr', 'agent', 'prompt', codex_pane, busy_prompt, '--wait', '--until',
                'working', '--until', 'done', '--until', 'blocked', '--timeout', '15000')
        wait_for('busy native tool started', start.exists, seconds=60)
        assert not finished.exists(), 'busy window elapsed before queue probe'
        command('codex', 'queue', '--thread', codex_ref, '--message',
                'Queued while busy. Preserve the running tool and finish the existing task. '
                'Then reply exactly KANBI_BUSY_QUEUE_OK. Do not cancel any work.')
        command('herdr', 'pane', 'wait-output', codex_pane, '--regex',
                r'(?m)^\s*(?:•\s*)?KANBI_BUSY_QUEUE_OK\s*$', '--source', 'recent',
                '--timeout', '120000', timeout=130)
        assert finished.exists(), 'queued notice interrupted the original tool'
        print('PASS: native Codex queue processed a busy notice after the owned tool completed')

        (root / 'messages.json').write_text(json.dumps(db.execute(
            'select id,sender,recipient,state,reply_to,body from session_messages order by id'
        ).fetchall(), indent=2))
        print('PASS: real Codex send -> idle native Pi wake -> inbox/ack/reply -> native Codex queue wake -> inbox/ack')
        success = True
    finally:
        if db:
            db.close()
        subprocess.run(['python3', str(fixture), 'stop', str(runtime)], env=base_env, check=True)
        if success:
            shutil.rmtree(root)
        else:
            print('Failure artifacts retained:', root)


if __name__ == '__main__':
    main()
