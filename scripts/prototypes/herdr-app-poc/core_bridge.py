#!/usr/bin/env python3
"""Disposable GH-404 bridge. Never point an existing Kanbi DB at this binary.
Only the private PoC config may use this narrow Herdr CLI compatibility surface.
Core terminals are independent, never kept/recovered into replacement shells.
"""
import fcntl
import json
import os
from pathlib import Path
import select
import struct
import subprocess
import sys
import uuid

BASE = Path(os.environ.get('KANBI_APP_POC_HOME', str(Path.home()/'.local/share/kanbi/herdr-app-poc'))).resolve()
HC = str(Path.home()/'.local/bin/hc')
APP = str(Path.home()/'.local/bin/herdr-app')
ENV = dict(os.environ, HC_SESSION='kanbi-poc', HERDR_APP_HOME=str(BASE/'app'))
for key in ['HC_TERMINAL_ID','HC_PARENT','HERDR_PANE_ID','HERDR_TAB_ID','HERDR_WORKSPACE_ID','HERDR_CLIENT_SOCKET_PATH','HERDR_SOCKET_PATH']:
    ENV.pop(key, None)


def rpc(method, params=None):
    payload = json.dumps(dict(jsonrpc='2.0', id=1, method=method, params=params or {}))+'\n'
    p = subprocess.run([HC,'connect'],input=payload,text=True,capture_output=True,env=ENV,timeout=25)
    for line in p.stdout.splitlines():
        r = json.loads(line)
        if r.get('id') == 1:
            if 'error' in r:
                raise RuntimeError(json.dumps(r['error']))
            return r['result']
    raise RuntimeError(p.stderr or p.stdout or 'core did not respond')


def app(command, params):
    p = subprocess.run([APP,'do',command,json.dumps(params)],capture_output=True,text=True,env=ENV,timeout=15)
    if p.returncode:
        raise RuntimeError(p.stdout or p.stderr)
    return json.loads(p.stdout)


def send_bytes(address, data, size=None, inspect=None):
    """Attach without resize, send literal bytes, detach with acknowledged CLOSE."""
    current=rpc('terminal.get',{'terminal':address})
    current=current.get('terminal',current)
    p = subprocess.Popen([HC,'connect'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=ENV)
    def exact(n):
        out=b''
        while len(out)<n:
            if not select.select([p.stdout],[],[],8)[0]: raise TimeoutError('core stream timeout')
            b=os.read(p.stdout.fileno(), n-len(out))
            if not b: raise RuntimeError('core stream ended')
            out+=b
        return out
    def frame(ch,kind,payload):
        p.stdin.write(struct.pack('<IBI',ch,kind,len(payload))+payload); p.stdin.flush()
    def receive():
        ch,kind,n=struct.unpack('<IBI',exact(9))
        if n>16*1024*1024: raise RuntimeError('oversized frame')
        return ch,kind,exact(n)
    try:
        p.stdin.write(b'{"jsonrpc":"2.0","id":1,"method":"hello","params":{"framed":true}}\n');p.stdin.flush()
        line=b''
        while not line.endswith(b'\n'): line+=exact(1)
        hello=json.loads(line)
        if 'error' in hello: raise RuntimeError(str(hello))
        frame(0,0,json.dumps(dict(jsonrpc='2.0',id=2,method='terminal.attach',params={'terminal':address,'cols':size[0] if size else current['cols'],'rows':size[1] if size else current['rows']})).encode())
        while True:
            ch,kind,payload=receive()
            if ch==0 and kind==0:
                r=json.loads(payload)
                if r.get('id')==2:
                    if 'error' in r: raise RuntimeError(str(r['error']))
                    channel=r['result']['channel'];break
        frame(channel,0,data)
        if inspect: inspect()
        frame(channel,2,b'')
        while True:
            ch,kind,payload=receive()
            if ch==channel and kind==2: break
            if ch==channel and kind==0: frame(channel,1,struct.pack('<I',len(payload)))
    finally:
        p.terminate()
        try:p.communicate(timeout=2)
        except subprocess.TimeoutExpired:p.kill();p.communicate()


def option(args, flag, default=''):
    return args[args.index(flag)+1] if flag in args else default


def error(kind, message):
    print(json.dumps({'error':{'code':kind,'message':message}}))
    raise SystemExit(1)


def terminal(state, pane):
    m=state['panes'].get(pane)
    if not m or not m.get('terminal'): error('pane_not_found','No launched PoC terminal')
    t=rpc('terminal.get',{'terminal':m['terminal']})
    t=t.get('terminal',t)
    if t.get('labels',{}).get('kanbi.poc.pane')!=pane or t.get('state')!='live':
        error('pane_not_found','The original PoC program has ended or ownership changed')
    return m,t


def main():
    args=sys.argv[1:]
    if args[:3]==['agent','start','--help']:
        print('PoC bridge: agent start --kind KIND --pane ID (canonical launch deliberately unsupported; use configured explicit executable)');return
    BASE.mkdir(parents=True,exist_ok=True)
    with open(BASE/'bridge.lock','a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        path=BASE/'bridge.json'
        state=json.loads(path.read_text()) if path.exists() else {'workspaces':{},'panes':{}}
        def save():
            tmp=path.with_suffix('.tmp');tmp.write_text(json.dumps(state));tmp.replace(path)
        result={}
        pair=args[:2]
        if args and args[0]=='status':
            s=rpc('server.status');result={'server':{'running':True,'compatible':True,'socket':str(BASE/'prototype-not-a-herdr-socket'),'session':'kanbi-poc','core_id':s['core']['id']}}
            print(json.dumps(result));return
        if pair==['workspace','list']:result={'workspaces':list(state['workspaces'].values())}
        elif pair==['workspace','create']:
            wid='poc-w-'+uuid.uuid4().hex[:10]
            w={'workspace_id':wid,'cwd':option(args,'--cwd',str(BASE/'project')),'label':option(args,'--label','Kanbi PoC')}
            state['workspaces'][wid]=w;save();result={'workspace':w}
        elif pair==['workspace','get']:
            if args[2] not in state['workspaces']:error('workspace_not_found','Unknown PoC workspace')
            result={'workspace':state['workspaces'][args[2]]}
        elif pair==['tab','create']:
            pane='poc-p-'+uuid.uuid4().hex
            state['panes'][pane]={'pane_id':pane,'tab_id':'poc-tab-'+pane,'workspace_id':option(args,'--workspace'),'label':option(args,'--label'),'cwd':option(args,'--cwd',str(BASE/'project')),'env':{}}
            for i,a in enumerate(args):
                if a=='--env':
                    k,v=args[i+1].split('=',1);state['panes'][pane]['env'][k]=v
            save();result={'root_pane':state['panes'][pane], 'tab':{'tab_id':'poc-tab-'+pane}}
        elif pair==['pane','run']:
            pane=args[2];m=state['panes'][pane]
            if m.get('terminal'):raise RuntimeError('PoC pane already launched')
            # Start one actual core program, not an intermediate shell pane.
            out=rpc('terminal.run',dict(argv=['/bin/bash','-c',args[3]],cwd=m['cwd'],env=m['env'],no_parent=True,no_notify=True,keep=False,labels={'kanbi.poc.pane':pane,'kanbi.poc.owner':'gh404'},name='poc-'+pane[-16:],size={'cols':100,'rows':32}))
            t=out.get('terminal',out);m['terminal']=t['address'];save();result={'pane':m,'terminal_id':t['address']}
        elif pair==['pane','get']:
            m,t=terminal(state,args[2]);result={'pane':dict(m,terminal_id=t['address'],scroll={'viewport_rows':t.get('rows',32)})}
        elif pair in (['agent','get'],['agent','read'],['pane','read']):
            m,t=terminal(state,args[2])
            if args[1]=='get':result={'agent':{'name':args[2],'agent_status':(t.get('status') or {}).get('state','unknown'),'message':(t.get('status') or {}).get('msg','')}}
            else:
                r=rpc('terminal.read',{'terminal':t['address'],'lines':int(option(args,'--lines','100'))})
                result={'read':{'text':r['text']}}
        elif pair==['pane','send-text']:
            m,t=terminal(state,args[2]);send_bytes(t['address'], b'\x1b[200~'+args[3].encode()+b'\x1b[201~')
        elif pair==['pane','send-keys']:
            m,t=terminal(state,args[2]);keys=[{'Enter':'enter','Escape':'esc','C-c':'ctrl+c','C-d':'ctrl+d'}.get(k,k.lower()) for k in args[3:]]
            rpc('terminal.keys',{'terminal':t['address'],'keys':keys})
        elif pair in (['agent','focus'],['pane','focus'],['tab','focus']):
            pane=option(args,'--pane',args[2] if len(args)>2 else '')
            if pair==['tab','focus']:pane=pane.removeprefix('poc-tab-')
            m,t=terminal(state,pane)
            # The custom layout displays the selected terminal directly, so source tabs stay intact.
            app('layout.set',{'layout':layout_for(t['address'])})
            app('pane.focus',{'terminal':t['address']})
        elif pair in (['pane','close'],['tab','close']):
            pane=args[2].removeprefix('poc-tab-')
            m=state['panes'].get(pane)
            if m and m.get('terminal'):
                try:
                    _,t=terminal(state,pane);rpc('terminal.kill',{'terminal':t['address']})
                except SystemExit:pass
        elif pair in (['pane','rename'],['tab','rename']):
            pass
        else:raise RuntimeError('Unsupported PoC bridge operation: '+repr(args[:3]))
        print(json.dumps({'result':result}))


def layout_for(address):
    return {'id':'root','type':'row','theme':'graphite','font_size':14,'terminal_padding':[12,10], 'children':[
      {'id':'kanbi-board','type':'block','size':{'weight':1.2},'min':520,'content':{'mod':'kanbi-poc'}},
      {'id':'agent-area','type':'block','size':{'weight':1},'min':360,'content':{'terminal':address}}]}

if __name__=='__main__':
    try:main()
    except Exception as e:error('poc_bridge_error',str(e))
