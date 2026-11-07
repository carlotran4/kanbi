#!/usr/bin/env python3
"""Verify installed Pi RPC/native wake using an offline fixture model.

Owns temporary Kanbi/Pi state. The sender is synthetic; this is not authenticated
cross-harness acceptance. Does not access real sessions, credentials, or network.
"""
import json
import os
from pathlib import Path
import queue
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time

PROVIDER = '''
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai/compat";
export default function(pi) {
  pi.registerProvider("kanbi-offline", {
    baseUrl: "http://invalid.local", apiKey: "test-only", api: "openai-completions",
    models: [{ id: "fixture", name: "fixture", reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 32768, maxTokens: 1024 }],
    streamSimple(model) {
      const stream = createAssistantMessageEventStream();
      const message = {
        role: "assistant", content: [{ type: "text", text: "KANBI_NATIVE_WAKE_OK" }],
        api: model.api, provider: model.provider, model: model.id,
        usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "stop", timestamp: Date.now(),
      };
      queueMicrotask(() => {
        stream.push({ type: "start", partial: message });
        stream.push({ type: "done", reason: "stop", message });
        stream.end();
      });
      return stream;
    },
  });
}
'''


def main():
    repo = Path(__file__).resolve().parents[2]
    root = Path(tempfile.mkdtemp(prefix='kanbi-396-pi-'))
    binary = os.environ.get('KANBI_SMOKE_BIN', str(root / 'kanbi'))
    if 'KANBI_SMOKE_BIN' not in os.environ:
        build_env = dict(os.environ, GOCACHE=os.environ.get('GOCACHE', '/tmp/kanbi-go-build'))
        subprocess.run(['go', 'build', '-o', binary, './cmd/kanbi'], cwd=repo,
                       env=build_env, check=True)
    env = dict(os.environ, KANBI_DB=str(root / 'kanbi.db'),
               KANBI_CONFIG=str(root / 'config.yaml'), KANBI_STATE_DIR=str(root / 'state'),
               KANBI_DATA_DIR=str(root / 'data'), PI_CODING_AGENT_DIR=str(root / 'pi'),
               KANBI_MAILBOX_CLI=binary)
    (root / 'config.yaml').write_text('multiplexer:\n  default: herdr\n')

    def cli(*args):
        return subprocess.check_output([binary, *args], env=env, text=True, timeout=10)

    cli('add', 'sender', '--harness', 'codex')
    cli('add', 'recipient', '--harness', 'pi')
    db = sqlite3.connect(root / 'kanbi.db')
    for ticket, harness in db.execute('select id,harness from tickets').fetchall():
        db.execute("insert into sessions(ticket_id,harness,tmux_session_name,tmux_window_name,"
                   "multiplexer,status,is_active,created_at,updated_at) "
                   "values(?,?,'','','herdr','running',1,datetime('now'),datetime('now'))",
                   (ticket, harness))
    db.commit()
    env['KANBI_SESSION_ID'] = '1'
    sent = json.loads(cli('messages', 'send', '2', '--key', 'native-pi', '--body', 'native wake probe'))
    env['KANBI_SESSION_ID'] = '2'
    endpoint_dir = Path(str(root / 'kanbi.db') + '.messages')
    endpoint_dir.mkdir(mode=0o700)
    env['KANBI_MESSAGE_ENDPOINT_FILE'] = str(endpoint_dir / '2.json')
    provider = root / 'provider.ts'
    provider.write_text(PROVIDER)
    stderr = open(root / 'stderr.log', 'w')
    log = open(root / 'events.log', 'w')
    process = subprocess.Popen(
        ['pi', '--mode', 'rpc', '--no-extensions', '-e', str(provider), '-e',
         str(repo / 'scripts/prototypes/pi-mailbox.mjs'), '--provider', 'kanbi-offline',
         '--model', 'fixture', '--session-dir', str(root / 'sessions')],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr, env=env,
        text=True, cwd=root)
    try:
        lines = queue.Queue()

        def read_events():
            for line in process.stdout:
                lines.put(line)

        threading.Thread(target=read_events, daemon=True).start()
        deadline = time.monotonic() + 20
        events = []
        while time.monotonic() < deadline:
            try:
                line = lines.get(timeout=.25)
            except queue.Empty:
                if process.poll() is not None:
                    break
                continue
            log.write(line)
            log.flush()
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                continue
            if events[-1].get('type') == 'agent_end':
                break
        deadline = time.monotonic() + 6
        while time.monotonic() < deadline:
            state = db.execute('select state from session_messages').fetchone()[0]
            if state == 'notified':
                break
            time.sleep(.1)
        assert any(e.get('type') == 'agent_start' for e in events), 'idle session never woke'
        assert any(e.get('type') == 'agent_end' for e in events), 'native turn did not finish'
        assert state == 'notified', 'native acceptance not persisted'
        cli('messages', 'ack', str(sent['message']['id']))
        cli('messages', 'reply', str(sent['message']['id']), '--key', 'native-reply',
            '--body', 'native wake observed')
        env['KANBI_SESSION_ID'] = '1'
        assert json.loads(cli('messages', 'inbox'))[0]['body'] == 'native wake observed'
        print('PASS: installed Pi native idle wake, notification, CLI ack/reply '
              '(offline model; synthetic sender)')
    except BaseException:
        print('Failure artifacts retained:', root)
        print((root / 'stderr.log').read_text()[-2000:])
        raise
    finally:
        process.terminate()
        try:
            process.wait(5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        log.close()
        stderr.close()
        db.close()
    shutil.rmtree(root)


if __name__ == '__main__':
    main()
