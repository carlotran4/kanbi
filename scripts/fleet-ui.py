#!/usr/bin/env python3
"""Verify Fleet with interactive fake processes on an owned isolated Herdr server."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import shlex
import signal
import sqlite3
import struct
import subprocess
import sys
import tempfile
import termios
import time

sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parent.parent
UI = ROOT / '.agents/skills/kanbi-ui-validation/scripts/ui-session.sh'
spec = importlib.util.spec_from_file_location('herdr_fixture', ROOT / 'scripts/herdr-fixture.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


def main():
    with tempfile.TemporaryDirectory(prefix='kanbi-fleet-') as temporary:
        work = Path(temporary) / 'ui'
        env = dict(os.environ, KANBI_UI_WORKDIR=str(work))
        started = False
        db = None
        def ui(*args):
            return subprocess.run([str(UI), *args], cwd=ROOT, env=env, check=True,
                                  capture_output=True, text=True, timeout=90).stdout
        try:
            ui('start')
            started = True
            record = json.loads((work / 'herdr-runtime.json').read_text())
            isolated = {k: v for k, v in os.environ.items() if not k.startswith('HERDR_')}
            isolated.update(HERDR_SOCKET_PATH=record['socket'],
                            HERDR_CONFIG_PATH=str(work / 'herdr-fixture/config.toml'))
            def herdr(*args):
                output = subprocess.run(['herdr', *args], env=isolated, check=True,
                                        capture_output=True, text=True, timeout=20).stdout
                return json.loads(output)['result'] if output.startswith('{') else output
            def wait(check, reason, timeout=10):
                deadline = time.monotonic() + timeout
                while time.monotonic() < deadline:
                    result = check()
                    if result:
                        return result
                    time.sleep(.05)
                print('FAILED:', reason, 'PHASE:', (journal() or {}).get('Phase'), 'FRAME:', visible(), flush=True)
                raise AssertionError(reason)
            def visible():
                return herdr('pane', 'read', record['pane'], '--source', 'visible')
            def key(value):
                herdr('pane', 'send-keys', record['pane'], value)
            def journal():
                row = db.execute('select payload from fleet_journal').fetchone()
                return json.loads(row[0]) if row else None
            def layout():
                return herdr('pane', 'layout', '--pane', record['pane'])['layout']
            def shape(frame):
                return {p['pane_id']: p['rect'] for p in frame['panes']}
            def outer_input(text):
                fixture.request(work / 'herdr-fixture', {'action': 'input', 'text': text})
            def resize(width, height):
                fixture.request(work / 'herdr-fixture', {'action': 'resize', 'width': width, 'height': height})
                wait(lambda: layout()['area']['height'] == height, 'client resize did not settle')
            fake = work / 'fake-agent.py'
            fake.write_text('''import os,sys
print("PID",os.getpid(),flush=True)
print("PROMPT_READY",flush=True)
for line in sys.stdin:
 print("ECHO",line.strip(),flush=True)
''')
            db = sqlite3.connect(work / 'kanbi-ui-test.db')
            tickets = db.execute('select id,board_id from tickets where display_number in (4,8) order by board_id,id limit 6').fetchall()
            remote = herdr('workspace', 'create', '--cwd', str(work), '--label', 'Fleet remote', '--no-focus')
            agents = []
            for i, (ticket, board) in enumerate(tickets):
                workspace = record['workspace'] if i < 3 else remote['workspace']['workspace_id']
                tab = herdr('tab', 'create', '--workspace', workspace, '--cwd', str(work),
                            '--label', f'Fleet agent {i}', '--no-focus')
                pane = tab['root_pane']
                herdr('pane', 'run', pane['pane_id'], shlex.join(['exec', 'python3', '-u', str(fake)]))
                wait(lambda: 'PROMPT_READY' in herdr('pane', 'read', pane['pane_id'], '--source', 'visible'), 'fake agent not ready')
                meta = json.dumps({k: pane[k] for k in ('pane_id', 'tab_id', 'terminal_id')})
                session = db.execute('''insert into sessions(ticket_id,harness,tmux_session_name,tmux_window_name,
                  multiplexer,mux_namespace,mux_container_id,mux_container_name,mux_metadata,status,is_active,
                  started_at,created_at,updated_at) values(?,'pi','','','herdr',?,?,?,?,'running',0,
                  CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)''',
                  (ticket, workspace, pane['pane_id'], f'Fleet agent {i}', meta)).lastrowid
                agents.append(dict(ticket=ticket, board=board, session=session, pane=pane))
            db.commit()
            total_sessions = db.execute('select count(*) from sessions').fetchone()[0]
            key('enter')
            wait(lambda: 'MASTER' in visible(), 'Master did not open')
            def membership(count):
                for i, agent in enumerate(agents):
                    db.execute("update sessions set is_active=?,status=?,updated_at=CURRENT_TIMESTAMP where id=?",
                               (int(i < count), 'running' if i < count else 'closed', agent['session']))
                db.commit()
            def enter():
                key('v')
                wait(lambda: 'GATHER FLEET' in visible(), 'Fleet summary missing')
                key('enter')
                wait(lambda: journal() and journal()['Phase'] == 'active', 'Fleet entry did not complete', 20)
                wait(lambda: '[Return to board' in visible() and 'moving panes' not in visible(), 'Fleet header missing or still entering')
                return journal()
            def returned(snapshot):
                wait(lambda: journal() is None, 'Fleet return journal not cleared', 20)
                wait(lambda: len(layout()['panes']) == 1, 'board pane not restored')
                assert db.execute('select count(*) from sessions').fetchone()[0] == total_sessions
                for member in snapshot['Members']:
                    row = db.execute('select mux_metadata from sessions where id=?', (member['Session'],)).fetchone()
                    pane = herdr('pane', 'get', json.loads(row[0])['pane_id'])['pane']
                    assert pane['terminal_id'] == member['Pane']['terminal_id'], 'terminal replaced'
                    assert pane['tab_id'] == member['Source']['tab_id'], 'wrong source tab'
            for count in (3, 4, 5, 6):
                membership(count)
                snapshot = enter()
                frame = layout()
                rects = {p['pane_id']: p['rect'] for p in frame['panes']}
                areas = [rects[m['Pane']['pane_id']]['width'] * rects[m['Pane']['pane_id']]['height'] for m in snapshot['Members']]
                assert max(areas) / min(areas) < 1.13, f'unequal pane areas for {count}: {areas}'
                assert frame['focused_pane_id'] == record['pane'], 'header not focused after entry'
                member = snapshot['Members'][0]
                pane_id = member['Pane']['pane_id']
                if count == 3:
                    # Verify the installed Herdr CLI actually retains inherited
                    # flock descriptors for the lifetime of an in-flight request.
                    lock_path = str(work / 'kanbi-ui-test.db.runtime.lock')
                    held = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
                    fcntl.flock(held, fcntl.LOCK_EX)
                    child = subprocess.Popen(['herdr', 'pane', 'wait-output', pane_id,
                                              '--match', 'NO_SUCH_FLEET_LOCK_PROBE',
                                              '--source', 'visible', '--timeout', '750'],
                                             env=isolated, pass_fds=(held,),
                                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                    os.close(held)
                    time.sleep(.1)
                    assert child.poll() is None, 'Herdr lock probe exited too soon'
                    contender = os.open(lock_path, os.O_RDWR)
                    try:
                        try:
                            fcntl.flock(contender, fcntl.LOCK_EX | fcntl.LOCK_NB)
                        except BlockingIOError:
                            pass
                        else:
                            raise AssertionError('Herdr CLI dropped inherited runtime ownership')
                    finally:
                        os.close(contender)
                    child.communicate(timeout=5)
                    print('installed Herdr CLI retains in-flight operation lock: pass', flush=True)

                herdr('pane', 'send-text', pane_id, 'FLEET_DIRECT_INPUT')
                herdr('pane', 'send-keys', pane_id, 'enter')
                wait(lambda: 'ECHO FLEET_DIRECT_INPUT' in herdr('pane', 'read', pane_id, '--source', 'visible'), 'input did not reach harness')
                # Use the rendering client's actual native bindings, not synthetic app input.
                outer_input('\x02\t')
                wait(lambda: layout()['focused_pane_id'] != record['pane'], 'native cycle focus failed')
                outer_input('\x02z')
                wait(lambda: layout()['zoomed'], 'native zoom failed')
                outer_input('\x02z')
                wait(lambda: not layout()['zoomed'], 'native unzoom failed')
                assert shape(frame) == shape(layout()), 'zoom changed slots'
                # Cycle through every pane until the header is reached again.
                for _ in range(len(frame['panes']) + 1):
                    if layout()['focused_pane_id'] == record['pane']:
                        break
                    outer_input('\x02\t')
                    time.sleep(.05)
                assert layout()['focused_pane_id'] == record['pane'], 'keyboard-only header return failed'
                if count == 6:
                    resize(80, 24)
                    wait(lambda: '[Return to board' in visible(), '80x24 return control clipped')
                    assert len(layout()['panes']) == len(frame['panes']), 'resize changed membership'
                    print('compact header rectangle:', layout()['panes'][0]['rect'], flush=True)
                # Mouse activation travels through the outer Herdr client and terminal protocol.
                width = 80 if count == 6 else 160
                with open((work / 'terminal-tty').read_text().strip(), 'rb', buffering=0) as tty:
                    content_rows, _, _, _ = struct.unpack('HHHH', fcntl.ioctl(tty, termios.TIOCGWINSZ, b'\0'*8))
                header_rect = layout()['panes'][0]['rect']
                mouse_row = header_rect['y'] + header_rect['height'] - content_rows + 1
                if count == 6:
                    print('compact header content rows / mouse row:', content_rows, mouse_row, flush=True)
                outer_input(f'\x1b[<0;{width - 2};{mouse_row}M\x1b[<0;{width - 2};{mouse_row}m')
                wait(lambda: journal() is None, 'mouse Return did not activate', 20)
                returned(snapshot)
                if count == 6:
                    with open((work / 'terminal-tty').read_text().strip(), 'rb', buffering=0) as tty:
                        rows, cols, _, _ = struct.unpack('HHHH', fcntl.ioctl(tty, termios.TIOCGWINSZ, b'\0'*8))
                        assert (cols, rows) == (80, 24)
                    key('v')
                    wait(lambda: 'larger terminal' in visible(), 'small-terminal entry did not refuse six agents')
                    assert journal() is None
                    for small_count in (1, 2):
                        membership(small_count)
                        small = enter()
                        assert len(small['Members']) == small_count
                        assert '[Return to board' in visible()
                        key('v')
                        returned(small)
                        print(f'80x24 readable {small_count}-agent entry and return: pass', flush=True)
                    membership(6)
                    resize(160, 45)
                print(f'{count} agents: equal live panes, direct input, native focus/zoom, mouse return: pass', flush=True)
            # Authoritative pane disappearance must leave every other slot fixed.
            snapshot = enter()
            before = shape(layout())
            lost = snapshot['Members'][0]['Pane']['pane_id']
            herdr('pane', 'close', lost)
            wait(lambda: lost not in shape(layout()), 'pane close did not settle')
            after = shape(layout())
            for member in snapshot['Members'][1:]:
                assert after[member['Pane']['pane_id']] == before[member['Pane']['pane_id']], 'agent exit shifted a surviving slot'
            key('v')
            wait(lambda: journal() is None, 'return after exit failed', 20)
            print('agent disappearance preserves surviving slot rectangles: pass', flush=True)
            # A new Kanbi process must recover after SIGKILL, without relaunching agents.
            membership(5)
            # The lost attempt must remain inactive; use the surviving five.
            db.execute('update sessions set is_active=0 where id=?', (snapshot['Members'][0]['Session'],))
            db.execute('update sessions set is_active=1 where id=?', (agents[5]['session'],))
            db.commit()
            interrupted = enter()
            # Model a split whose server side effect completed before its reply
            # could be committed. The creation environment nonce proves ownership.
            marker = interrupted['Token'] + '-lost-reply'
            pending = dict(interrupted, Phase='entering', PendingAnchor={
                'Marker': marker, 'TargetTab': record['tab'],
                'Before': [p['terminal_id'] for p in herdr('pane', 'list')['panes']]})
            db.execute('update fleet_journal set payload=? where id=1', (json.dumps(pending),))
            db.commit()
            unrecorded = herdr('pane', 'split', record['pane'], '--direction', 'down',
                               '--ratio', '0.5', '--env', f'KANBI_FLEET_ANCHOR={marker}',
                               '--no-focus')['pane']
            info = herdr('pane', 'process-info', '--pane', record['pane'])
            process = info['process_info']['shell_pid']
            os.kill(process, signal.SIGKILL)
            wait(lambda: all(p['pane_id'] != record['pane'] for p in herdr('pane', 'list')['panes']), 'killed board pane survived')
            tab = herdr('tab', 'create', '--workspace', record['workspace'], '--cwd', str(work), '--label', 'Fleet recovery', '--focus')
            record['pane'], record['tab'] = tab['root_pane']['pane_id'], tab['tab']['tab_id']
            (work / 'herdr-runtime.json').write_text(json.dumps(record))
            command = shlex.join(['exec', 'env', 'KANBI_INNER=1', f'KANBI_CONFIG={work / "config.yaml"}',
                                 f'KANBI_DB={work / "kanbi-ui-test.db"}', f'KANBI_STATE_DIR={work / "state"}',
                                 f'KANBI_DATA_DIR={work / "data"}', str(work / 'kanbi'), '--board'])
            herdr('pane', 'run', record['pane'], command)
            wait(lambda: 'RESTORE FLEET PANES' in visible(), 'startup recovery prompt missing')
            key('enter')
            returned(interrupted)
            assert all(p['terminal_id'] != unrecorded['terminal_id'] for p in herdr('pane', 'list')['panes']), 'unacknowledged anchor leaked'
            print('SIGKILL/startup recovery preserves exact terminals/attempts and cleans lost split reply: pass', flush=True)
        finally:
            if db:
                db.close()
            if started:
                ui('stop')


if __name__ == '__main__':
    main()
