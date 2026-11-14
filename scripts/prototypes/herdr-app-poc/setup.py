#!/usr/bin/env python3
"""Install only the isolated, opt-in GH-404 PoC. Never import a live Kanbi DB."""
import json
import os
from pathlib import Path
import shutil
import subprocess

SRC=Path(__file__).resolve().parent
BASE=Path.home()/'.local/share/kanbi/herdr-app-poc'
BASE.mkdir(parents=True,exist_ok=True)
os.chmod(BASE,0o700)
for name in ['bin','project','state','data','app/mods/kanbi-poc','app/layouts']:(BASE/name).mkdir(parents=True,exist_ok=True)
for name in ['core_bridge.py','demo_agent.py','codex_agent.py','ui.py']:
    shutil.copy2(SRC/name,BASE/'bin'/name)
    (BASE/'bin'/name).chmod(0o755)
shutil.copy2(SRC/'main.js',BASE/'app/mods/kanbi-poc/main.js')
(BASE/'app/mods/kanbi-poc/mod.json').write_text(json.dumps({'place':'none','title':'Kanbi proof of concept','focusable':True}))
repo=SRC.parents[2]
subprocess.run(['go','build','-o',str(BASE/'bin/kanbi'),'./cmd/kanbi'],cwd=repo,check=True)
# The pi label is retained as the supported configurable harness slot. UI calls
# this Offline demo explicitly; no fake conversation refs or safe resume claim.
cfg={'db_path':str(BASE/'kanbi.db'),'default_harness':'pi','multiplexer':{'default':'herdr','herdr':{'binary':str(BASE/'bin/core_bridge.py'),'session':'kanbi-poc','focus_on_open':True}},'focus':{'enabled':False},'graceful_exit_timeout_seconds':2,'harnesses':{h:{'start':[str(BASE/'bin/demo_agent.py')],'start_with_prompt':[str(BASE/'bin/demo_agent.py')],'resume':['/usr/bin/false'],'prompt_mode':'arg','exit':['quit','Enter'],'session_ref':'POC_NO_RESUME_REF='} for h in ['pi','codex','claude','copilot']}}
codex=shutil.which('codex')
if codex:
    (BASE/'codex.json').write_text(json.dumps({'binary':codex}))
    cfg['harnesses']['codex']={'start':[str(BASE/'bin/codex_agent.py')],'start_with_prompt':[str(BASE/'bin/codex_agent.py')],'resume':['/usr/bin/false'],'prompt_mode':'arg','exit':['C-c','C-c'],'session_ref':'POC_NO_RESUME_REF='}
# JSON is also YAML, and avoids a new dependency in this standalone installer.
(BASE/'config.yaml').write_text(json.dumps(cfg,indent=2))
env=dict(os.environ,KANBI_CONFIG=str(BASE/'config.yaml'),KANBI_DB=str(BASE/'kanbi.db'),KANBI_STATE_DIR=str(BASE/'state'),KANBI_DATA_DIR=str(BASE/'data'),KANBI_INNER='1',HC_SESSION='kanbi-poc',HERDR_APP_HOME=str(BASE/'app'))
def k(*args):return subprocess.run([str(BASE/'bin/kanbi'),*args],env=env,text=True,capture_output=True,check=True).stdout
boards=json.loads(k('boards','--json')).get('boards',[])
if not any(b['name']=='App Sandbox' for b in boards):
    k('boards','add','App Sandbox','--cwd',str(BASE/'project'))
    k('boards','disable-sync','App Sandbox')
    for title,body in [('Try the live agent beside the board','Offline demo: open this ticket, then type work or permission. No model requests.'),('Create and move a ticket','Use the title field to create a ticket. Select a card, then use Move to review.'),('Explore the full Kanbi TUI','Use Full TUI to open the existing board in an actual core terminal.')]:
        k('add',title,'--body',body,'--harness','pi','--board','App Sandbox','--json')
(BASE/'app/layouts/kanbi-poc.json').write_text(json.dumps({'id':'root','type':'row','theme':'graphite','children':[{'id':'kanbi-board','type':'block','size':{'weight':1},'content':{'mod':'kanbi-poc'}}]},indent=2))
settings=BASE/'app/settings.json'
s=json.loads(settings.read_text()) if settings.exists() else {}
s.update({'telemetry':False,'core':str(Path.home()/'.local/bin/hc')})
settings.write_text(json.dumps(s,indent=2))
launcher=Path.home()/'.local/bin/kanbi-herdr-poc'
launcher.write_text('#!/bin/sh\nexport HC_SESSION=kanbi-poc\nexport HERDR_APP_HOME='+str(BASE/'app')+'\nexport HERDR_TELEMETRY=0\nexec '+str(Path.home()/'.local/bin/herdr-app')+' "$@"\n')
launcher.chmod(0o755)
desktop=Path.home()/'.local/share/applications/kanbi-herdr-poc.desktop'
desktop.write_text('[Desktop Entry]\nType=Application\nName=Kanbi Herdr POC\nComment=Private Kanbi sandbox in Herdr app\nExec='+str(launcher)+'\nIcon=herdr-app\nTerminal=false\nCategories=Development;\n')
print(json.dumps({'home':str(BASE),'launch':str(launcher),'board':'App Sandbox'}))
