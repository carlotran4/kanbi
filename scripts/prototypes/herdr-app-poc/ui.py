#!/usr/bin/env python3
"""Structured native-mod actions, restricted to the private sandbox."""
import json
import os
from pathlib import Path
import subprocess
import sys
from core_bridge import BASE, ENV, rpc, app

ENV.update(KANBI_CONFIG=str(BASE/'config.yaml'),KANBI_DB=str(BASE/'kanbi.db'),KANBI_STATE_DIR=str(BASE/'state'),KANBI_DATA_DIR=str(BASE/'data'),KANBI_INNER='1')
BOARD='App Sandbox'

def kanbi(*args):
    p=subprocess.run([str(BASE/'bin/kanbi'),*args],env=ENV,capture_output=True,text=True,timeout=50)
    if p.returncode:raise RuntimeError(p.stderr or p.stdout)
    return json.loads(p.stdout) if p.stdout.strip().startswith(('{','[')) else {'message':p.stdout.strip()}

def snapshot():
    out=kanbi('list','--board',BOARD,'--json')
    bridge=json.loads((BASE/'bridge.json').read_text()) if (BASE/'bridge.json').exists() else {'panes':{}}
    live=rpc('terminal.list',{'all':True})
    return {'tickets':out.get('tickets',[]),'terminals':live.get('terminals',[]),'panes':bridge['panes'],'board':BOARD}

def main():
    action=sys.argv[1] if len(sys.argv)>1 else 'snapshot'
    params=json.loads(sys.argv[2]) if len(sys.argv)>2 else {}
    tid=params.get('ticket','')
    if tid and not (tid.startswith('T-') and tid[2:].isdigit()):raise ValueError('Invalid ticket')
    if action=='snapshot':return snapshot()
    if action=='add':return kanbi('add',params['title'],'--body','Created in the Herdr app POC. Offline demo only.','--board',BOARD,'--harness','pi','--json')
    if action=='codex':
        if not (BASE/'codex.json').exists():raise RuntimeError('Codex is not installed')
        return kanbi('add','Try real Codex in Herdr app','--body','For this first test turn, reply exactly KANBI_CORE_POC_OK. Do not run commands, read files, use tools, or edit anything. Then wait for further instructions.','--board',BOARD,'--harness','codex','--json')
    if action=='move':
        column=params['column']
        if column not in ['Open','In Progress','Review','Done']:raise ValueError('Invalid column')
        return kanbi('move',tid,'--to',column,'--board',BOARD,'--json')
    if action=='note':return kanbi('notes','add',tid,'--body',params['body'],'--board',BOARD,'--json')
    if action=='show':return kanbi('show',tid,'--board',BOARD,'--json')
    if action=='open':return kanbi('open',tid,'--board',BOARD,'--json')
    if action=='tui':
        # One normal board TUI supplies Kanbi's existing watcher and settings.
        listed=rpc('terminal.list',{'labels':{'kanbi.poc.tui':'true'}})
        ts=listed.get('terminals',[])
        if ts:address=ts[0]['address']
        else:
            out=rpc('terminal.run',{'argv':[str(BASE/'bin/kanbi'),'--board'],'cwd':str(BASE/'project'),'env':{k:ENV[k] for k in ['KANBI_CONFIG','KANBI_DB','KANBI_STATE_DIR','KANBI_DATA_DIR','KANBI_INNER']},'no_parent':True,'keep':False,'labels':{'kanbi.poc.tui':'true'},'size':{'cols':160,'rows':45}})
            address=out.get('terminal',out)['address']
        app('layout.set',{'layout':{'id':'root','type':'row','theme':'graphite','font_size':14,'children':[{'id':'kanbi-board','type':'block','size':{'weight':.8},'min':440,'content':{'mod':'kanbi-poc'}},{'id':'full-tui','type':'block','size':{'weight':1.2},'min':700,'content':{'terminal':address}}]}})
        return {'terminal':address}
    if action=='board':
        app('layout.set',{'layout':{'id':'root','type':'row','theme':'graphite','children':[{'id':'kanbi-board','type':'block','content':{'mod':'kanbi-poc'}}]}})
        return {'message':'Board expanded'}
    raise ValueError('Unsupported action')

if __name__=='__main__':
    try:print(json.dumps(main()))
    except Exception as e:print(json.dumps({'error':str(e)}));sys.exit(1)
