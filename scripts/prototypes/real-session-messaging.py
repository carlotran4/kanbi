#!/usr/bin/env python3
"""Opt-in finite Pi/Codex exchange in owned Herdr panes; may consume quota.

Exercises normal automatic Kanbi delivery. The driver does not send native wake
notices: managed launches install the Pi receiver and the CLI routes Codex mail. Native histories
use the installed harness configuration, as in real-harness-lifecycle.sh.
"""
import importlib.util
import fcntl
import shlex
import struct
import termios
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
    # Keep native tool-manager installs/config separate from the fixture's XDG
    # isolation; otherwise mise redownloads every harness into each test directory.
    base_env['MISE_DATA_DIR'] = os.environ.get('MISE_DATA_DIR', str(Path(os.environ.get('XDG_DATA_HOME', str(Path.home() / '.local/share'))) / 'mise'))
    base_env['MISE_CONFIG_DIR'] = os.environ.get('MISE_CONFIG_DIR', str(Path(os.environ.get('XDG_CONFIG_HOME', str(Path.home() / '.config'))) / 'mise'))
    binary = str(root / 'kanbi')
    subprocess.run(['go', 'build', '-o', binary, './cmd/kanbi'], cwd=REPO, env=base_env, check=True)
    # JSON arrays and strings are YAML-compatible. All paths belong to this fixture.
    pi_wrapper = root / 'bin' / 'pi'
    pi_wrapper.parent.mkdir()
    pi_wrapper.write_text('#!/bin/sh\ntty > ' + shlex.quote(str(root / 'pi-tty')) +
                          '\nexec ' + shlex.quote(os.path.realpath(shutil.which('pi'))) + ' "$@"\n')
    pi_wrapper.chmod(0o700)
    pi_command = [str(pi_wrapper)]
    codex_command = ['codex', '--no-alt-screen', '--ask-for-approval', 'never',
                     '--sandbox', 'workspace-write']
    (root / 'config.yaml').write_text(
        'multiplexer:\n  default: herdr\nprompt_ready_timeout: 60s\nharnesses:\n'
        + '  pi:\n    start: ' + json.dumps(pi_command) + '\n'
        + '  codex:\n    start: ' + json.dumps(codex_command) + '\n'
        + '  copilot:\n    start: [copilot, --allow-all-tools]\n    start_with_prompt: [copilot, --allow-all-tools, -i]\n')
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
            'KANBI_SENDER_WAITING. Do not poll or wait for a response in this turn. '
            'When the later mailbox reply KANBI_PI_REPLY_OK wakes you, read it, acknowledge '
            'it, and finish with exactly KANBI_MESSAGE_REPLY_OK without replying.'
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
        # No driver notification: the recipient's reply command must wake Codex.
        command('herdr', 'pane', 'wait-output', codex_pane, '--regex',
                r'(?m)^\s*(?:•\s*)?KANBI_MESSAGE_REPLY_OK\s*$', '--source', 'recent',
                '--timeout', '120000', timeout=130)
        wait_for('Codex acknowledged reply', lambda: db.execute(
            "select id from session_messages where sender=? and recipient=? and state='acknowledged'",
            (pi_id, codex_id)).fetchone())
        def send_as(sender, target, key, body):
            result = subprocess.run([binary, 'messages', 'send', str(target), '--key', key,
                                     '--body', body], env=dict(env, KANBI_SESSION_ID=str(sender)),
                                    cwd=root, capture_output=True, text=True, timeout=10)
            if result.returncode:
                raise RuntimeError(result.stderr)
            return json.loads(result.stdout)['message']['id']

        def assert_pi_size(width, height):
            tty_path = (root / 'pi-tty').read_text().strip()
            deadline = time.monotonic() + 5
            with open(tty_path, 'rb', buffering=0) as terminal:
                while time.monotonic() < deadline:
                    rows, cols, _, _ = struct.unpack('HHHH', fcntl.ioctl(terminal, termios.TIOCGWINSZ, b'\0' * 8))
                    if (cols, rows) == (width, height):
                        return
                    time.sleep(.1)
            raise RuntimeError(f'owned Pi PTY is {cols}x{rows}, expected {width}x{height}')

        # Type a draft only into the owned Codex pane. Queue notification must
        # preserve it; it may run immediately or defer until the draft is cleared.
        draft = 'KANBI_UNSENT_DRAFT_396'
        command('herdr', 'pane', 'send-text', codex_pane, draft)
        draft_message = send_as(pi_id, codex_id, 'draft-safety',
                'Read and acknowledge this Kanbi message, then reply exactly KANBI_DRAFT_QUEUE_OK '
                'in your terminal. Do not modify any draft text or send a mailbox reply.')
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
        wait_for('draft probe acknowledged', lambda: db.execute(
            "select id from session_messages where id=? and state='acknowledged'", (draft_message,)).fetchone())
        print('PASS: automatic Codex notification preserved the owned unsent draft')

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
        send_as(pi_id, codex_id, 'busy-safety',
                'Queued while busy. Preserve the running tool and finish the existing task. '
                'Read and acknowledge this Kanbi message, then reply exactly KANBI_BUSY_QUEUE_OK '
                'in your terminal. Do not cancel any work or send a mailbox reply.')
        command('herdr', 'pane', 'wait-output', codex_pane, '--regex',
                r'(?m)^\s*(?:•\s*)?KANBI_BUSY_QUEUE_OK\s*$', '--source', 'recent',
                '--timeout', '120000', timeout=130)
        assert finished.exists(), 'queued notice interrupted the original tool'
        print('PASS: native Codex queue processed a busy notice after the owned tool completed')

        # Verify the interactive Pi receiver at 80x24: retain typed human input,
        # defer activation, then deliver automatically after only that draft clears.
        pi_pane = db.execute("select json_extract(mux_metadata,'$.pane_id') from sessions where id=?", (pi_id,)).fetchone()[0]
        pi_tab = db.execute("select json_extract(mux_metadata,'$.tab_id') from sessions where id=?", (pi_id,)).fetchone()[0]
        command('herdr', 'tab', 'focus', pi_tab)
        subprocess.run(['python3', str(fixture), 'resize', str(runtime), '80', '24'], env=base_env, check=True)
        assert_pi_size(80, 24)
        pi_draft = 'KANBI_PI_UNSENT_396'
        command('herdr', 'pane', 'send-text', pi_pane, pi_draft)
        pi_message = send_as(codex_id, pi_id, 'pi-draft-safety',
                'Read and acknowledge this message, then print exactly KANBI_PI_DRAFT_OK in '
                'your terminal. Do not send a mailbox reply.')
        time.sleep(3)
        pending = db.execute('select state,notification_status from session_messages where id=?', (pi_message,)).fetchone()
        assert pending[0] == 'queued' and pending[1] in ('pending', 'deferred'), 'Pi activated over an unsent draft'
        pi_screen = command('herdr', 'pane', 'read', pi_pane, '--source', 'visible', '--format', 'text')
        assert pi_draft in pi_screen, 'Pi draft was lost'
        command('herdr', 'pane', 'send-keys', pi_pane, *(['backspace'] * len(pi_draft)))
        wait_for('Pi draft probe automatically delivered', lambda: db.execute(
            "select id from session_messages where id=? and state='acknowledged'", (pi_message,)).fetchone())
        command('herdr', 'pane', 'wait-output', pi_pane, '--regex',
                r'(?m)^\s*KANBI_PI_DRAFT_OK\s*$', '--source', 'recent', '--timeout', '60000')
        subprocess.run(['python3', str(fixture), 'resize', str(runtime), '160', '45'], env=base_env, check=True)
        assert_pi_size(160, 45)
        print('PASS: interactive Pi retained and deferred over a draft at 80x24, then automatically delivered')

        if os.environ.get('KANBI_REAL_COPILOT_TESTS') == '1':
            kanbi('add', 'Native Copilot receiver', '--harness', 'copilot', '--body',
                  'Disposable mailbox test. Reply exactly KANBI_COPILOT_READY. Do not use tools or modify files.')
            kanbi('open', 'T-003', '--send-prompt')
            copilot_id, copilot_pane = db.execute(
                "select id,json_extract(mux_metadata,'$.pane_id') from sessions where harness='copilot'").fetchone()
            copilot_frame = command('herdr', 'pane', 'read', copilot_pane, '--source', 'recent', '--format', 'text')
            if '403' in copilot_frame and ('unauthorized' in copilot_frame or 'not authorized' in copilot_frame):
                (root / 'copilot-blocker.txt').write_text(copilot_frame)
                raise RuntimeError('Copilot live response blocked by account/organization policy: model catalog HTTP 403')
            command('herdr', 'pane', 'wait-output', copilot_pane, '--regex',
                    r'(?m)^\s*(?:[•●]\s*)?KANBI_COPILOT_READY\s*$', '--source', 'recent', '--timeout', '120000', timeout=130)
            command('herdr', 'agent', 'wait', copilot_pane, '--until', 'idle', '--until', 'done', '--timeout', '30000')
            copilot_message = send_as(pi_id, copilot_id, 'copilot-fallback',
                    'Read and acknowledge this Kanbi message, then print exactly KANBI_COPILOT_MESSAGE_OK. '
                    'Do not send a mailbox reply or edit project files.')
            wait_for('Copilot terminal fallback read and acknowledgement', lambda: db.execute(
                "select id from session_messages where id=? and state='acknowledged'", (copilot_message,)).fetchone())
            print('PASS: Copilot literal text-and-Enter fallback -> inbox/ack')

        (root / 'messages.json').write_text(json.dumps(db.execute(
            'select id,sender,recipient,state,reply_to,body from session_messages order by id'
        ).fetchall(), indent=2))
        print('PASS: automatic real Codex send -> idle interactive Pi wake -> inbox/ack/reply -> automatic Codex wake -> inbox/ack')
        success = True
    finally:
        if db and not success:
            for harness, pane in db.execute("select harness,json_extract(mux_metadata,'$.pane_id') from sessions"):
                if pane:
                    frame = subprocess.run(['herdr', 'pane', 'read', pane, '--source', 'recent',
                                             '--format', 'text', '--lines', '60'], env=env,
                                            capture_output=True, text=True)
                    (root / f'{harness}-frame.txt').write_text(frame.stdout)
        if db:
            db.close()
        subprocess.run(['python3', str(fixture), 'stop', str(runtime)], env=base_env, check=True)
        if success:
            shutil.rmtree(root)
        else:
            print('Failure artifacts retained:', root)


if __name__ == '__main__':
    main()
