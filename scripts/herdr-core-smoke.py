#!/usr/bin/env python3
"""Opt-in real app/core smoke. Owns its core session, app home, DB and processes.
Usage: python3 scripts/herdr-core-smoke.py /path/to/kanbi
Requires installed hc/herdr-app, Python3 and a graphical desktop. No real agents.
Artifacts remain in /tmp; cleanup stops only this fixture's window and core.
"""
import json
import os
import re
from pathlib import Path
import select
import shutil
import sqlite3
import struct
import subprocess
import sys
import tempfile
import time
import uuid


def main():
    binary = str(Path(sys.argv[1]).resolve())
    root = Path(tempfile.mkdtemp(prefix='kanbi-core-smoke-'))
    hc = shutil.which('hc')
    app = shutil.which('herdr-app')
    assert hc and app, 'Install hc and herdr-app'
    env = dict(os.environ, HC_SESSION='kanbi-smoke-'+uuid.uuid4().hex[:12],
               HERDR_APP_HOME=str(root/'app'), HERDR_TELEMETRY='0',
               NO_COLOR='',  # This graphical fixture must not inherit the automation runner's no-color flag.
               KANBI_CONFIG=str(root/'config.yaml'), KANBI_DB=str(root/'kanbi.db'),
               KANBI_STATE_DIR=str(root/'state'), KANBI_DATA_DIR=str(root/'data'))
    for k in ['HC_TERMINAL_ID', 'HC_PARENT', 'HERDR_SOCKET_PATH', 'HERDR_CLIENT_SOCKET_PATH',
              'HERDR_PANE_ID', 'HERDR_TAB_ID', 'HERDR_WORKSPACE_ID', 'HERDR_ENV', 'KANBI_INNER']:
        env.pop(k, None)
    program = root/'agent.py'
    program.write_text("""#!/usr/bin/env python3
import os,sys,tty
if 'resume' in sys.argv: print('RESUMED_FIXTURE',flush=True)
print('SESSION_REF=core-fixture-ref',flush=True)
print('PROMPT_READY',flush=True)
print('\\x1b]7501;state=idle:app=kanbi-fixture\\x1b\\\\',end='',flush=True)
tty.setraw(0)
while True:
 b=os.read(0,4096)
 if not b or b'\\x03' in b: break
 os.write(1,b)
""")
    program.chmod(0o700)
    config = {'multiplexer': {'default':'herdr-core', 'herdr_core':{
        'binary':hc, 'app_binary':app, 'session':env['HC_SESSION'], 'focus_on_open':True}},
        'harnesses': {'pi':{'start':[str(program)],
            'resume':[str(program),'resume','{session_ref}'],
            'prompt_mode':'arg','session_ref':'SESSION_REF=','exit':['C-c']}}}
    (root/'config.yaml').write_text(json.dumps(config))

    def command(argv, timeout=35):
        p = subprocess.run(argv, env=env, cwd=root, capture_output=True, text=True, timeout=timeout)
        if p.returncode:
            raise RuntimeError(f'{argv}: {p.stdout}\n{p.stderr}')
        return p.stdout

    def rpc(method, params=None):
        p = subprocess.run([hc,'connect'], env=env, input=json.dumps({
            'jsonrpc':'2.0','id':1,'method':method,'params':params or {}})+'\n',
            capture_output=True, text=True, timeout=25)
        for line in p.stdout.splitlines():
            r = json.loads(line)
            if r.get('id') == 1:
                assert 'error' not in r, r
                return r['result']
        raise RuntimeError(p.stderr or p.stdout)

    def until(fn, description, seconds=15):
        deadline=time.monotonic()+seconds
        while time.monotonic()<deadline:
            result=fn()
            if result: return result
            time.sleep(.15)
        raise AssertionError(description)

    def rows():
        with sqlite3.connect(f'file:{root}/kanbi.db?mode=ro',uri=True) as db:
            db.row_factory=sqlite3.Row
            return [dict(r) for r in db.execute('select * from sessions order by id')]

    def screen(address):
        return rpc('terminal.read',{'terminal':address,'lines':0})['text']

    def exact_size(address, cols, lines, action):
        paint=bytearray()
        p=subprocess.Popen([hc,'connect'],env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE)
        def exact(n):
            out=b''
            while len(out)<n:
                assert select.select([p.stdout],[],[],10)[0], 'stream timeout'
                part=os.read(p.stdout.fileno(),n-len(out))
                assert part, 'stream ended'
                out+=part
            return out
        def write(ch,kind,payload):
            p.stdin.write(struct.pack('<IBI',ch,kind,len(payload))+payload);p.stdin.flush()
        def read():
            ch,k,n=struct.unpack('<IBI',exact(9));assert n<=16*1024*1024
            return ch,k,exact(n)
        try:
            p.stdin.write(b'{"jsonrpc":"2.0","id":1,"method":"hello","params":{"framed":true}}\n');p.stdin.flush()
            line=b''
            while not line.endswith(b'\n'):line+=exact(1)
            assert 'result' in json.loads(line)
            write(0,0,json.dumps({'jsonrpc':'2.0','id':2,'method':'terminal.attach','params':{
                'terminal':address,'cols':cols,'rows':lines}}).encode())
            ch,k,data=read();assert ch==0 and k==0
            channel=json.loads(data)['result']['channel']
            until(lambda: rpc('terminal.get',{'terminal':address})['cols']==cols,'resize cols')
            action()
            write(channel,2,b'')
            while True:
                ch,k,data=read()
                if ch==channel and k==2:break
                if ch==channel and k==0:
                    paint.extend(data)
                    write(channel,1,struct.pack('<I',len(data)))
            return bytes(paint)
        finally:
            p.terminate();p.communicate(timeout=5)

    (root/'app').mkdir()
    (root/'app/settings.json').write_text(json.dumps({'core':hc,'telemetry':False}))
    (root/'app/shared.json').write_text(json.dumps({'frame.welcomed':'finished'}))
    window=None
    log=(root/'app.log').open('w')
    try:
        rpc('hello',{'generation':1})
        window=subprocess.Popen([app],env=env,stdout=log,stderr=log,start_new_session=True)
        def app_ready():
            p=subprocess.run([app,'status'],env=env,capture_output=True,text=True,timeout=5)
            return p.returncode==0
        until(app_ready,'app startup',30)
        command([binary,'doctor'])
        command([binary,'add','Core smoke ticket','--harness','pi','--body','Fixture prompt'])
        command([binary,'open','T-001'])
        first=rows()[-1];assert first['multiplexer']=='herdr-core'
        agent=first['mux_container_id'];original=rpc('terminal.get',{'terminal':agent})
        assert original['parent'] is None and not original['keep']
        command([binary,'open','T-001'])
        assert len(rows())==1 and rpc('terminal.get',{'terminal':agent})['pid']==original['pid']
        # Exercise outside-host startup, which must show the unchanged board.
        command([binary])
        all_terms=rpc('terminal.list')['terminals']
        board=next(t['address'] for t in all_terms if t['labels'].get('kanbi.title')=='Kanbi')
        until(lambda: 'Master' in screen(board),'board picker')
        rpc('terminal.keys',{'terminal':board,'keys':['enter']})
        until(lambda: 'Core smoke ticket' in screen(board),'Master board')
        for cols,lines in [(160,45),(80,24)]:
            def check():
                def frame():
                    t=rpc('terminal.get',{'terminal':board})
                    text=screen(board)
                    (root/f'last-{cols}x{lines}.txt').write_text(text)
                    return text if t['cols']==cols and t['rows']==lines and 'Core smoke ticket' in text and 'Kanbi' in text and '?' in text else None
                text=until(frame,f'board header/footer at {cols}x{lines}')
                (root/f'board-{cols}x{lines}.txt').write_text(text)
            paint=exact_size(board,cols,lines,check)
            (root/f'board-{cols}x{lines}.ansi').write_bytes(paint)
            # A text-only snapshot cannot detect NO_COLOR regressions. Inspect
            # the real VT paint, requiring explicit foreground AND filled cards.
            sgr=re.findall(rb'\x1b\[([0-9;:]*)m',paint)
            assert any(re.search(rb'(^|[;:])38[;:](2|5)[;:]',v) for v in sgr), f'foreground colors missing at {cols}x{lines}'
            assert any(re.search(rb'(^|[;:])48[;:](2|5)[;:]',v) for v in sgr), f'card background fills missing at {cols}x{lines}'
            backgrounds={m.group(0) for v in sgr for m in re.finditer(rb'48;(?:2;[0-9]+;[0-9]+;[0-9]+|5;[0-9]+)',v)}
            assert len(backgrounds)>=2, f'header and selected-card backgrounds must remain distinct at {cols}x{lines}'
        # Enter must focus the same agent; x must close that attempt without loss.
        rpc('terminal.keys',{'terminal':board,'keys':['enter']})
        agent_tab=rpc('terminal.get',{'terminal':agent})['labels']['herdr.tab']
        until(lambda:json.loads(command([app,'status'])).get('tab')==agent_tab,'TUI Enter focus')
        assert len(rows())==1 and rows()[0]['id']==first['id']
        command([app,'do','pane.focus',json.dumps({'terminal':board})])
        focus=json.loads(command([app,'status']))['focused_pane']
        command([binary,'update','T-001','--title','Core smoke ticket renamed'])
        until(lambda:'core-smoke-ticket-renamed' in command([app,'ls']),'tab title update')
        assert json.loads(command([app,'status']))['focused_pane']==focus, 'rename changed focus'
        rpc('terminal.keys',{'terminal':board,'keys':['x']})
        until(lambda: not rows()[0]['is_active'],'TUI close')
        assert rows()[0]['status']=='closed' and rows()[0]['harness_session_ref']=='core-fixture-ref'
        command([binary,'open','T-001'])
        second=rows()[-1];assert len(rows())==2 and second['id']!=first['id']
        until(lambda: 'RESUMED_FIXTURE' in screen(second['mux_container_id']),'fixture resume')
        # Closing/reopening the GUI must preserve all live program identities.
        before={t['address']:t['pid'] for t in rpc('terminal.list')['terminals']}
        command([app,'do','window.close'])
        window.wait(timeout=10)
        window=subprocess.Popen([app],env=env,stdout=log,stderr=log,start_new_session=True)
        until(app_ready,'app restart',30)
        after={t['address']:t['pid'] for t in rpc('terminal.list')['terminals']}
        assert all(after.get(k)==v for k,v in before.items())
        status=json.loads(command([app,'status']));assert not status.get('errors'),status
        until(lambda: board in command([app,'ls']), 'board adopted after app restart')
        command([app,'do','pane.focus',json.dumps({'terminal':board})])
        time.sleep(.8)  # Let the newly attached GUI terminal paint before its screenshot.
        until(lambda: 'runtime refresh stale' not in screen(board), 'refresh after lifecycle lock')
        command([app,'shot',str(root/'board.png')])
        # Kill the original program; the board watcher must reconcile its attempt.
        rpc('terminal.kill',{'terminal':second['mux_container_id']})
        until(lambda: not rows()[-1]['is_active'],'missing program reconciliation')
        assert len(rows())==2 and rows()[0]['id']==first['id']
        real = next((arg.split('=',1)[1] for arg in sys.argv[2:] if arg.startswith('--real-harnesses=')), '')
        if real:
            assert os.environ.get('KANBI_REAL_HARNESS_TESTS')=='1', 'Authenticated model turns require KANBI_REAL_HARNESS_TESTS=1'
            config['harnesses']={}
            (root/'config.yaml').write_text(json.dumps(config))
            for harness in real.split(','):
                assert harness in ['pi','codex','copilot','claude']
                added=json.loads(command([binary,'add','Actual '+harness,'--harness',harness,
                    '--body','Reply with exactly KANBI_CORE_REAL_OK. Do not use tools, read files or modify anything.','--json']))
                display=added['ticket']['display_id']
                command([binary,'open',display],timeout=60)
                attempt=rows()[-1]
                address=attempt['mux_container_id']
                response=re.compile(r'^\s*(?:•\s*)?KANBI_CORE_REAL_OK\s*$',re.M)
                until(lambda:response.search(screen(address)),harness+' real response',120)
                command([binary,'open',display])
                attempt=rows()[-1]
                assert attempt['harness_session_ref'], harness+' did not capture a verified ref'
                rpc('terminal.kill',{'terminal':address})
                command([binary,'open',display],timeout=60)
                resumed=rows()[-1]
                assert resumed['id']!=attempt['id'] and resumed['harness_session_ref']==attempt['harness_session_ref']
                until(lambda:response.search(screen(resumed['mux_container_id'])),harness+' resumed conversation output',40)
                rpc('terminal.kill',{'terminal':resumed['mux_container_id']})
                print(harness+': actual response, verified ref, resume and restored conversation: PASS',flush=True)
        print(json.dumps({'passed':True,'artifacts':str(root),'checks':[
            'doctor','native app focus','same PID/attempt on reopen','unchanged TUI at 160x45 and 80x24','foreground colors and card fills at both sizes',
            'TUI Enter focus','tab rename without focus change','TUI close','fixture verified-ref resume/history','app restart persistence','missing program reconciliation']}))
    finally:
        if window:
            try:command([app,'do','window.close']);window.wait(timeout=10)
            except Exception:
                if window.poll() is None:window.terminate();window.wait(timeout=10)
        try:command([hc,'server','stop'])
        finally:log.close();print('Artifacts:',root,file=sys.stderr)

if __name__=='__main__':
    main()
