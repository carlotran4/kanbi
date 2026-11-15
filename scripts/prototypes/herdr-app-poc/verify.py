#!/usr/bin/env python3
"""Real-core smoke of the installed private PoC; never uses a production DB.
Creates one offline ticket and preserves its ended attempt for inspection.
"""
import json
import sqlite3
import subprocess
import sys
import time
import uuid
from pathlib import Path

BASE=Path.home()/'.local/share/kanbi/herdr-app-poc'
sys.path.insert(0,str(BASE/'bin'))
from core_bridge import rpc, send_bytes, terminal
from ui import kanbi

def eventually(predicate, seconds=8):
    until=time.monotonic()+seconds
    while time.monotonic()<until:
        value=predicate()
        if value:return value
        time.sleep(.15)
    raise AssertionError('condition did not become true')

def main():
    assert (BASE/'config.yaml').exists(), 'Install the PoC first'
    c=sqlite3.connect('file:'+str(BASE/'kanbi.db')+'?mode=ro',uri=True)
    assert c.execute("select count(*) from boards where ticket_backend not in ('local','')").fetchone()[0]==0
    title='POC verification '+uuid.uuid4().hex[:8]
    made=kanbi('add',title,'--body','Offline verification only.','--harness','pi','--board','App Sandbox','--json')['ticket']
    tid=made['display_id'];ticket_id=made['id']
    opened=kanbi('open',tid,'--board','App Sandbox','--json')['ticket']
    pane=opened['session']['mux_container_id']
    bridge=json.loads((BASE/'bridge.json').read_text());address=bridge['panes'][pane]['terminal']
    before=rpc('terminal.get',{'terminal':address})
    assert before['parent'] is None and not before['keep']
    kanbi('open',tid,'--board','App Sandbox','--json')
    assert rpc('terminal.get',{'terminal':address})['pid']==before['pid']
    assert c.execute('select count(*) from sessions where ticket_id=?',(ticket_id,)).fetchone()[0]==1
    for word,state in [('permission','blocked'),('approve','idle'),('work','done')]:
        send_bytes(address,(word+'\r').encode())
        if word=='work':eventually(lambda:(rpc('terminal.get',{'terminal':address}).get('status')or{}).get('state')=='working')
        eventually(lambda:(rpc('terminal.get',{'terminal':address}).get('status')or{}).get('state')==state)
    literal='literal $(printf no-shell) `no-shell` ; "quoted"'
    send_bytes(address,(literal+'\r').encode())
    eventually(lambda:literal in rpc('terminal.read',{'terminal':address,'lines':30})['text'])
    kanbi('move',tid,'--to','Review','--board','App Sandbox','--json')
    kanbi('notes','add',tid,'--body','Verified private core lifecycle.','--board','App Sandbox','--json')
    shown=kanbi('show',tid,'--board','App Sandbox','--json')['ticket']
    assert shown['column']['name']=='Review'
    assert any(n['body']=='Verified private core lifecycle.' for n in shown['notes'])
    rpc('terminal.kill',{'terminal':address})
    # External program loss must not silently launch another conversation.
    try:kanbi('open',tid,'--board','App Sandbox','--json')
    except RuntimeError:pass
    else:raise AssertionError('Unverified ended session unexpectedly reopened')
    eventually(lambda:c.execute('select is_active from sessions where ticket_id=?',(ticket_id,)).fetchone()[0]==0)
    row=c.execute('select is_active,status,harness_session_ref from sessions where ticket_id=?',(ticket_id,)).fetchone()
    assert row[0]==0 and not row[2],row
    assert c.execute('select count(*) from sessions where ticket_id=?',(ticket_id,)).fetchone()[0]==1
    print(json.dumps({'passed':True,'ticket':tid,'checks':['independent process','same PID and one attempt on reopen','reported blocked idle working done','literal input','move and note persistence','program loss requires repair','history preserved']}))

if __name__=='__main__':main()
