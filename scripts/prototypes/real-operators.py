#!/usr/bin/env python3
"""Opt-in real Operator wake/reopen smoke. Owned DB, config and Herdr only."""
import importlib.util
import json
import os
from pathlib import Path
import shlex
import sqlite3
import subprocess
import tempfile
import time

REPO = Path(__file__).resolve().parents[2]

def main():
    if os.environ.get('KANBI_REAL_HARNESS_TESTS') != '1':
        print('Opt in: KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex python3 scripts/prototypes/real-operators.py')
        return
    root = Path(tempfile.mkdtemp(prefix='kanbi-real-operators-'))
    runtime = root / 'runtime'
    fixture = REPO / 'scripts/herdr-fixture.py'
    spec = importlib.util.spec_from_file_location('operators_fixture', fixture)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    env = {k: v for k, v in os.environ.items() if not k.startswith('HERDR_')}
    env.update(KANBI_CONFIG=str(root/'config.yaml'), KANBI_DB=str(root/'kanbi.db'),
               KANBI_STATE_DIR=str(root/'state'), KANBI_DATA_DIR=str(root/'data'), KANBI_INNER='1')
    # Preserve the installed harness manager while isolating Herdr's own XDG tree.
    env['MISE_DATA_DIR'] = os.environ.get('MISE_DATA_DIR', str(Path.home()/'.local/share/mise'))
    env['MISE_CONFIG_DIR'] = os.environ.get('MISE_CONFIG_DIR', str(Path.home()/'.config/mise'))
    binary = os.environ.get('KANBI_OPERATOR_TEST_BIN', str(root/'kanbi'))
    (root/'config.yaml').write_text('multiplexer:\n  default: herdr\nprompt_ready_timeout: 60s\n')
    build_env = dict(env)
    if env.get('GOTMPDIR'): build_env['TMPDIR'] = env['GOTMPDIR']
    if not os.environ.get('KANBI_OPERATOR_TEST_BIN'):
        subprocess.run(['go','build','-o',binary,'./cmd/kanbi'],cwd=REPO,env=build_env,check=True)
    db = None
    try:
        subprocess.run(['python3',str(fixture),'start',str(runtime),'160','45'],env=env,check=True)
        env.update({k:v for k,v in helper.environment(runtime).items() if k.startswith(('HERDR_','XDG_'))})
        def command(*args, timeout=150):
            p=subprocess.run(args,cwd=root,env=env,text=True,capture_output=True,timeout=timeout)
            if p.returncode: raise RuntimeError(f'{args[:2]}: {p.stderr[-2000:]} {p.stdout[-2000:]}')
            return p.stdout
        def kanbi(*args): return command(binary,*args)
        def wait(label,probe,seconds=150):
            end=time.monotonic()+seconds
            while time.monotonic()<end:
                v=probe()
                if v:return v
                time.sleep(1)
            raise RuntimeError('Timed out: '+label)
        kanbi('operators','list')
        db=sqlite3.connect(root/'kanbi.db')
        created=json.loads(command('herdr','workspace','create','--cwd',str(root),'--label','Operator test','--no-focus'))['result']
        pane=created['root_pane']['pane_id']
        launch=shlex.join(['exec','env',*[f'{k}={v}' for k,v in env.items() if k in ('KANBI_CONFIG','KANBI_DB','KANBI_STATE_DIR','KANBI_DATA_DIR','KANBI_INNER','MISE_DATA_DIR','MISE_CONFIG_DIR')],binary,'--board'])
        command('herdr','pane','run',pane,launch)
        for harness in os.environ.get('KANBI_REAL_HARNESSES','pi,codex').split(','):
            instructions="For initialization only, reply READY. For every wake whose notice contains Message ID: N, read this file and reply exactly KANBI_OPERATOR_OK_N, substituting N with the numeric message ID. Do not use tools except reading this instructions file, and do not modify files."
            op=json.loads(kanbi('operators','create','--name','Intake '+harness,'--harness',harness,'--cwd',str(root),'--instructions',instructions))
            oid=op['id']
            kanbi('operators','run',str(oid))
            session=wait(harness+' session',lambda:db.execute('select id,mux_container_id,mux_metadata from sessions where operator_id=? and is_active=1 and status<>?',(oid,'starting')).fetchone())
            sid,agent,metadata=session
            operator_pane=json.loads(metadata)['pane_id']
            wake=wait(harness+' queued wake',lambda:db.execute('select id from session_messages where recipient=? order by id limit 1',(-oid,)).fetchone())
            command('herdr','pane','wait-output',operator_pane,'--regex',f'KANBI_OPERATOR_OK_{wake[0]}','--source','recent','--timeout','120000')
            # Look for native acceptance rather than a marker in the echoed prompt.
            wait(harness+' wake receipt',lambda:db.execute("select 1 from session_messages where recipient=? and notification_status in ('notified','submitted')",(-oid,)).fetchone())
            # Direct file edits must affect the next wake in the same conversation.
            edited = instructions.replace('KANBI_OPERATOR_OK_N', 'KANBI_OPERATOR_EDITED_N')
            (root/'operators'/str(oid)/'instructions.md').write_text(edited)
            kanbi('operators','run',str(oid))
            next_wake=wait(harness+' edited wake',lambda:db.execute('select id from session_messages where recipient=? and id>? order by id limit 1',(-oid,wake[0])).fetchone())
            command('herdr','pane','wait-output',operator_pane,'--regex',f'KANBI_OPERATOR_EDITED_{next_wake[0]}','--source','recent','--timeout','120000')
            assert db.execute('select id from sessions where operator_id=? and is_active=1',(oid,)).fetchone()[0] == sid
            command('herdr','agent','wait',agent,'--until','done','--timeout','30000')
            kanbi('operators','close',str(oid))
            kanbi('operators','run',str(oid))
            latest=wait(harness+' automatic resume',lambda:db.execute('select id,harness_session_ref,mux_metadata from sessions where operator_id=? and is_active=1 and status<>? and id<>? order by id desc limit 1',(oid,'starting',sid)).fetchone())
            assert latest[0]!=sid and latest[1], latest
            resumed_pane=json.loads(latest[2])['pane_id']
            resumed_wake=wait(harness+' resumed wake',lambda:db.execute('select id from session_messages where recipient=? and id>? order by id limit 1',(-oid,next_wake[0])).fetchone())
            command('herdr','pane','wait-output',resumed_pane,'--regex',f'KANBI_OPERATOR_EDITED_{resumed_wake[0]}','--source','recent','--timeout','120000')
            print(harness+': PASS (Operator start, wake response, direct instruction reload, close, verified resume, stable identity)',flush=True)
        print('Artifacts: '+str(root),flush=True)
    finally:
        if db:db.close()
        subprocess.run(['python3',str(fixture),'stop',str(runtime)],env=env,check=False)
        print('Retained artifacts: '+str(root),flush=True)

if __name__=='__main__': main()
