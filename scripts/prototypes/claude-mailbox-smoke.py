#!/usr/bin/env python3
"""Verify the installed Claude socket/hook against a private offline model fixture.

No Claude account or network provider is used. Only the temporary Kanbi database
and owned Herdr pane are writable test targets. A finite local HTTP fixture emits
one Bash mailbox read/ack tool call and then a fixed assistant response.
"""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPO = Path(__file__).resolve().parents[2]


def main():
    if os.environ.get('KANBI_NATIVE_CLAUDE_TESTS') != '1':
        print('Opt in: KANBI_NATIVE_CLAUDE_TESTS=1 python3 scripts/prototypes/claude-mailbox-smoke.py')
        return
    root = Path(tempfile.mkdtemp(prefix='kanbi-claude-inbox-'))
    binary = str(root / 'kanbi')
    base = {k: v for k, v in os.environ.items() if not k.startswith('HERDR_')}
    base.update(KANBI_CONFIG=str(root / 'config.yaml'), KANBI_DB=str(root / 'kanbi.db'),
                KANBI_STATE_DIR=str(root / 'state'), KANBI_DATA_DIR=str(root / 'data'),
                CLAUDE_CONFIG_DIR=str(root / 'claude'), ANTHROPIC_API_KEY='sk-ant-test-fixture',
                CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',
                GOCACHE=os.environ.get('GOCACHE', '/tmp/kanbi-go-build'))
    # Keep native tool-manager installs/config separate from the fixture's XDG
    # isolation; otherwise mise redownloads every harness into each test directory.
    base['MISE_DATA_DIR'] = os.environ.get('MISE_DATA_DIR', str(Path(os.environ.get('XDG_DATA_HOME', str(Path.home() / '.local/share'))) / 'mise'))
    base['MISE_CONFIG_DIR'] = os.environ.get('MISE_CONFIG_DIR', str(Path(os.environ.get('XDG_CONFIG_HOME', str(Path.home() / '.config'))) / 'mise'))
    subprocess.run(['go', 'build', '-o', binary, './cmd/kanbi'], cwd=REPO, env=base, check=True)
    requests = []

    class Model(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))))
            if not self.path.startswith('/v1/messages'):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{}')
                return
            requests.append(body)
            messages = body.get('messages', [])
            completed_tool = any(part.get('type') == 'tool_result' for message in messages
                                 for part in (message.get('content') if isinstance(message.get('content'), list) else []))
            if completed_tool:
                block = {'type': 'text', 'text': 'KANBI_CLAUDE_SOCKET_OK'}
                stop = 'end_turn'
            else:
                block = {'type': 'tool_use', 'id': 'toolu_kanbi_fixture', 'name': 'Bash', 'input': {
                    'command': f'{binary} messages inbox && {binary} messages ack 1',
                    'description': 'Read and acknowledge the disposable Kanbi mailbox'}}
                stop = 'tool_use'
            response = {'id': f'msg_fixture_{len(requests)}', 'type': 'message', 'role': 'assistant',
                        'model': body.get('model', 'claude-sonnet-4-6'), 'content': [block],
                        'stop_reason': stop, 'stop_sequence': None,
                        'usage': {'input_tokens': 10, 'output_tokens': 10}}
            if not body.get('stream'):
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(response).encode())
                return
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.end_headers()
            start = dict(response, content=[], stop_reason=None)
            initial = dict(block, **({'text': ''} if block['type'] == 'text' else {'input': {}}))
            delta = ({'type': 'text_delta', 'text': block['text']} if block['type'] == 'text' else
                     {'type': 'input_json_delta', 'partial_json': json.dumps(block['input'])})
            for event in [
                {'type': 'message_start', 'message': start},
                {'type': 'content_block_start', 'index': 0, 'content_block': initial},
                {'type': 'content_block_delta', 'index': 0, 'delta': delta},
                {'type': 'content_block_stop', 'index': 0},
                {'type': 'message_delta', 'delta': {'stop_reason': stop, 'stop_sequence': None},
                 'usage': {'output_tokens': 10}},
                {'type': 'message_stop'},
            ]:
                self.wfile.write(f"event: {event['type']}\ndata: {json.dumps(event)}\n\n".encode())
                self.wfile.flush()

    server = ThreadingHTTPServer(('127.0.0.1', 0), Model)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base['ANTHROPIC_BASE_URL'] = f'http://127.0.0.1:{server.server_port}'
    command = ['claude', '--model', 'claude-sonnet-4-6', '--dangerously-skip-permissions',
               '--settings', json.dumps({'crossSessionInbound': 'accept'})]
    (root / 'claude').mkdir()
    (root / 'claude' / '.claude.json').write_text(json.dumps({
        'hasCompletedOnboarding': True, 'theme': 'dark', 'bypassPermissionsModeAccepted': True,
        'projects': {str(root): {'hasTrustDialogAccepted': True}},
    }))
    (root / 'config.yaml').write_text('multiplexer:\n  default: herdr\nharnesses:\n  claude:\n    start: ' + json.dumps(command) + '\n')
    fixture = REPO / 'scripts/herdr-fixture.py'
    spec = importlib.util.spec_from_file_location('claude_fixture', fixture)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    runtime = root / 'runtime'
    db = None
    success = False
    try:
        subprocess.run(['python3', str(fixture), 'start', str(runtime), '160', '45'], env=base, check=True)
        env = dict(base)
        env.update({k: v for k, v in helper.environment(runtime).items() if k.startswith(('HERDR_', 'XDG_'))})

        def cli(*args, identity=None):
            child = dict(env)
            if identity:
                child['KANBI_SESSION_ID'] = str(identity)
            result = subprocess.run([binary, *args], env=child, cwd=root, capture_output=True,
                                    text=True, timeout=30)
            if result.returncode:
                raise RuntimeError(result.stderr)
            return result.stdout

        cli('boards', 'set-cwd', 'Default', str(root))
        cli('add', 'Fixture sender', '--harness', 'pi')
        db = sqlite3.connect(root / 'kanbi.db')
        ticket = db.execute('select id from tickets').fetchone()[0]
        db.execute("insert into sessions(ticket_id,harness,tmux_session_name,tmux_window_name,"
                   "multiplexer,status,is_active,created_at,updated_at) "
                   "values(?,'pi','','','herdr','running',1,datetime('now'),datetime('now'))", (ticket,))
        db.commit()
        cli('add', 'Native Claude receiver', '--harness', 'claude')
        cli('open', 'T-002')
        recipient = db.execute("select id from sessions where harness='claude'").fetchone()[0]
        endpoint = Path(str(root / 'kanbi.db') + '.messages') / f'{recipient}.json'
        pane = db.execute("select json_extract(mux_metadata,'$.pane_id') from sessions where id=?", (recipient,)).fetchone()[0]
        deadline = time.monotonic() + 30
        approved_fixture_key = False
        while time.monotonic() < deadline and not endpoint.exists():
            frame = subprocess.run(['herdr', 'pane', 'read', pane, '--source', 'visible', '--format', 'text'],
                                   env=env, capture_output=True, text=True).stdout
            if 'Do you want to use this API key?' in frame and not approved_fixture_key:
                subprocess.run(['herdr', 'pane', 'send-keys', pane, 'up', 'enter'], env=env, check=True)
                approved_fixture_key = True
            time.sleep(.5)
        assert endpoint.exists(), 'SessionStart did not register the native Claude inbox'
        assert endpoint.stat().st_mode & 0o077 == 0, 'native credentials are not private'
        cli('messages', 'send', str(recipient), '--key', 'socket-test', '--body', 'Fixture mailbox work', identity=1)
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            state = db.execute('select state from session_messages where id=1').fetchone()[0]
            if state == 'acknowledged':
                break
            time.sleep(.2)
        assert state == 'acknowledged', f'Claude did not read and acknowledge native mail: {state}'
        assert requests, 'native inbox did not activate a model turn'
        print('PASS: installed Claude SessionStart registration -> native socket wake -> actual Bash inbox/ack '
              '(isolated offline model fixture, no authenticated provider)')
        success = True
    finally:
        if db and not success:
            for pane, in db.execute("select json_extract(mux_metadata,'$.pane_id') from sessions where harness='claude'"):
                if pane:
                    frame = subprocess.run(['herdr', 'pane', 'read', pane, '--source', 'recent',
                                             '--format', 'text', '--lines', '50'], env=env,
                                            capture_output=True, text=True)
                    (root / 'claude-frame.txt').write_text(frame.stdout)
                    print(frame.stdout[-3500:])
        if db:
            db.close()
        server.shutdown()
        server.server_close()
        subprocess.run(['python3', str(fixture), 'stop', str(runtime)], env=base, check=True)
        if success:
            shutil.rmtree(root)
        else:
            print('Failure artifacts retained:', root)


if __name__ == '__main__':
    main()
