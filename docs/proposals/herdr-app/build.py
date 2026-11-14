"""Build the self-contained proposal document and its editable visual concepts."""
from pathlib import Path
from html import escape
import markdown

ROOT = Path(__file__).resolve().parent
STYLE = '''
*{box-sizing:border-box}body{margin:0;color:#20252b;background:#eeefed;font:17px/1.65 system-ui,sans-serif}main{max-width:1220px;margin:36px auto;background:white;padding:64px 66px}h1{font-size:48px;line-height:1.12;letter-spacing:-1.5px;margin:0 0 16px;color:#111}h2{font-size:29px;line-height:1.25;margin:54px 0 18px;color:#111}h3{font-size:22px;margin:32px 0 12px;color:#111}p,li{max-width:88ch}a{color:#285c76}table{width:100%;border-collapse:collapse;font-size:14px;line-height:1.5;margin:24px 0}th,td{border:1px solid #d9d9d9;text-align:left;padding:13px;vertical-align:top}th{background:#e5edf0}tr:nth-child(even){background:#f6f8f9}figure{margin:28px 0 38px}figcaption{font-size:14px;color:#626c76;margin:12px 0}.window{background:#13191f;color:#d6dde4;border-radius:10px;overflow:hidden;font-size:12px;line-height:1.4;border:1px solid #303943}.chrome{height:38px;background:#252d35;padding:10px 16px;color:#afb9c3;display:flex;justify-content:space-between}.desk{display:grid;grid-template-columns:130px 1fr;min-height:500px}.nav{background:#192128;padding:18px 12px;border-right:1px solid #35404a}.nav b,.nav span{display:block;margin-bottom:18px}.nav .chosen{background:#34444e;color:white;padding:9px;border-radius:5px}.nav small{color:#8493a0}.surface{min-width:0}.toolbar{padding:15px 18px;border-bottom:1px solid #36414b;display:flex;justify-content:space-between;gap:10px;font-size:13px}.workspace{display:grid;grid-template-columns:1.2fr 1fr;min-height:455px}.board{padding:14px;min-width:0;overflow:auto}.cols{min-width:680px;display:grid;grid-template-columns:repeat(4,1fr);gap:8px}.column h4{margin:0 0 14px;padding-bottom:9px;border-bottom:1px solid #72818c;color:#bbc6cf;font-size:10px;letter-spacing:.5px}.card{background:#28333d;border-left:3px solid #53616b;padding:9px 7px;margin-bottom:10px;border-radius:3px;min-height:105px;overflow-wrap:anywhere}.card.sel{outline:1px solid #91afc2;background:#324451}.card.warn{border-left-color:#e1b96c}.card small{display:block;color:#aab7c0;font-size:10px}.card strong{display:block;margin:7px 0 12px;font-size:13px;font-weight:600}.card em{font-size:9px;color:#b6c7cc;font-style:normal}.terminal{background:#10161b;border-left:1px solid #45525e;min-width:0}.termhead{background:#27333d;padding:12px;font-size:11px;border-bottom:1px solid #506575}.termhead b{color:#e8c16e}.termtext{padding:16px;font:11px/1.9 ui-monospace,monospace;white-space:pre-wrap;overflow-wrap:anywhere;color:#b8c6cf}.termtext .bright{color:#ecf0f3}.termtext .amber{color:#e8c16e}.termtext .green{color:#9fc8a8}.prompt{border:1px solid #c4a967;padding:12px;margin:18px 0;color:#f1d298}.context{padding:20px;background:#1d2831}.context h4{font-size:17px;margin:4px 0 12px}.context p{font-size:12px;color:#b9c5cd}.tabs{padding:10px 16px;color:#afc3ce;border-bottom:1px solid #4a5b67}.review{display:grid;grid-template-columns:1fr 1.1fr 1fr}.diff{padding:14px;background:#15221c;font:10px/2 monospace;color:#9dc2a7;white-space:pre-wrap}.files{padding:14px;font-size:11px;color:#a9b8c5}.fleet{display:grid;grid-template-columns:170px 1fr}.rail{padding:14px;background:#1d2831}.tiles{display:grid;grid-template-columns:1fr 1fr;gap:10px;padding:12px}.tile{border:1px solid #465562;background:#10161b;min-height:185px}.tile .termtext{font-size:10px}.base{background:#151c22;color:#c3ccd3;padding:18px;overflow:auto;font:9px/1.35 ui-monospace,monospace;white-space:pre}.foot{padding:9px 16px;border-top:1px solid #36414b;color:#9baebc;font-size:10px}.overview{font-size:13px;letter-spacing:1px;color:#61717c;text-transform:uppercase}.diagram{display:flex;gap:10px;align-items:stretch;margin:25px 0;font-size:14px}.diagram div{flex:1;padding:18px;border:1px solid #d9d9d9;background:#fafbfc}.diagram b{display:block;margin-bottom:8px}.legend{font-size:13px;color:#596873}
@media(max-width:800px){main{padding:28px 20px;margin:0}h1{font-size:36px}.window{overflow:auto}.desk{min-width:940px}table{display:block;overflow:auto}.diagram{display:block}.diagram div{margin-bottom:8px}}
@media print{body{background:white;font-size:11pt}main{padding:0;margin:0;max-width:none}h1{font-size:32pt}h2{break-after:avoid;margin-top:28px}h3{break-after:avoid}figure{break-inside:avoid}table{font-size:9pt}tr{break-inside:avoid}.window{print-color-adjust:exact;-webkit-print-color-adjust:exact}.desk{min-height:420px}.base{font-size:6px}a{color:inherit} }
'''

def card(num,title,state='not started',sel=False,warn=False,harness='codex'):
    return f'<div class="card {"sel" if sel else ""} {"warn" if warn else ""}"><small>{num} · {state}</small><strong>{title}</strong><em>{harness}</em></div>'

def board():
    columns = [ ('OPEN · 2',[card('T-406','Clarify export behavior'),card('T-409','Improve attachment preview',harness='pi')]), ('IN PROGRESS · 2',[card('T-404','Investigate Herdr app','needs permission',True,True),card('T-408','Improve runtime detection','running',harness='claude')]), ('REVIEW · 1',[card('T-402','Polish board picker','closed',harness='pi')]), ('DONE · 1',[card('T-398','Preserve session history','closed')]) ]
    return '<div class="board"><div class="cols">'+''.join('<div class="column"><h4>'+name+'</h4>'+''.join(cards)+'</div>' for name,cards in columns)+'</div></div>'

NAV='<aside class="nav"><b>HERDR</b><small>SPACES</small><span class="chosen">Kanbi</span><span>Personal finance</span><span>Homelab</span><small>WORKSPACE</small><span>Board</span><span>Agents</span><span>Files</span><small>Kanbi owns ticket<br>workflow and history</small></aside>'

def window(content,toolbar='Kanbi / Board',footer='Board focus · Enter open agent · / search · i context · v fleet'):
    return '<div class="window"><div class="chrome"><span>● ● ● &nbsp; Herdr — Kanbi workspace</span><span>PROPOSED LAYOUT</span></div><div class="desk">'+NAV+'<div class="surface"><div class="toolbar"><b>'+toolbar+'</b><span>Board &nbsp; Context &nbsp; Review &nbsp; Fleet</span></div>'+content+'<div class="foot">'+footer+'</div></div></div></div>'

agent='''<div class="terminal"><div class="termhead">T-404 · codex · <b>needs permission</b><br>Kanbi · ticket worktree · existing attempt</div><div class="termtext"><span class="bright">Investigate Herdr app</span>

I have compared the core protocol with
Kanbi’s current runtime contract.

Next I’ll validate the lifecycle using
an isolated fake agent.
<div class="prompt">Allow this command?

  ./scripts/core-lifecycle-fixture.sh

  Approve once       Cancel</div><span class="amber">Harness interaction stays here.</span>
The board remains visible beside it.</div></div>'''
view1=window('<div class="workspace">'+board()+agent+'</div>')
view2=window('''<div class="review"><div class="context"><small>KANBI / T-404</small><h4>Investigate Herdr app</h4><p>needs permission · codex</p><div class="tabs">Context &nbsp; Notes &nbsp; History</div><h4>Next action</h4><p>Validate process ownership and recovery using a disposable runtime.</p><h4>Task</h4><p>Compare core with our current integration. Preserve history and safe resume.</p><h4>Worktree</h4><p>…/worktrees/445<br>branch: herdr-new-app-investigation</p><p>Open agent &nbsp; Session settings</p></div><div><div class="termhead">Ticket worktree · Changes</div><div class="files">M &nbsp; docs/herdr-app-investigation.md<br><br>Read-only diff · illustrative content</div><div class="diff">+ ## Runtime ownership
+
+ Ticket attempts remain independent
+ of the board process.
+
+ Recovering as a shell does not
+ resume the agent conversation.</div><div class="files">Review merge…<br><br>Runs Kanbi preflight and confirmation</div></div>'''+agent+'</div>','Kanbi / Ticket review','Review focus · exact ticket worktree · merge requires confirmation')

def tile(title,body):return '<div class="tile"><div class="termhead">'+title+'</div><div class="termtext">'+body+'</div></div>'
view3=window('<div class="fleet"><div class="rail"><b>Kanbi / Fleet</b><p>Visit membership fixed</p>'+card('T-404','Investigate Herdr app','needs permission',True,True)+card('T-408','Runtime detection','running',harness='claude')+'<p>Return to board</p><small>Existing attempts only<br>No agent relaunch</small></div><div class="tiles">'+tile('T-404 · codex · needs permission','Allow isolated lifecycle check?\n\nApprove once   Cancel')+tile('T-408 · claude · running','Inspecting native state transitions…\n\nChecking unknown status fallback.')+tile('T-411 · pi · waiting for user','The schema comparison is ready.\n\nWhich compatibility case comes next?')+tile('T-415 · copilot · running','Reviewing command error handling…\n\nPreserving last known state.')+'</div></div>','Kanbi / Fleet','Agent focus · native terminal input · zoom selected · Return to board restores layout')

md=(ROOT/'proposal.md').read_text()
body=markdown.markdown(md,extensions=['tables'])
for heading,view,caption in [('Board and selected agent',view1,'Concept 1 · Default desktop layout at a wide window size. The board remains visible during agent interaction.'),('Ticket context and review',view2,'Concept 2 · Ticket-bound context, an illustrative worktree diff and the same live attempt. The native review binding is proposed.'),('Fleet with the board retained',view3,'Concept 3 · Four existing live attempts with a compact Kanbi rail. Sample tile content is illustrative.')]:
    marker=f'<h3>{heading}</h3>'
    body=body.replace(marker,marker+'<figure>'+view+'<figcaption>'+caption+'</figcaption></figure>')
# Keep the document self-contained and usable without internet access.
diagram='''<div class="diagram"><div><b>Kanbi</b>Tickets · attempts · worktrees<br>Application services and SQLite</div><div><b>Herdr app</b>Native board mod · layout<br>Live terminal presentation and focus</div><div><b>Herdr core</b>Terminal processes · input/output<br>Program status and events</div></div>'''
body=body.replace('<h2>Product boundaries</h2>','<h2>Product boundaries</h2>'+diagram)
baseline=ROOT/'baseline-160x45.txt'
if not baseline.exists():
    baseline=Path('/tmp/kanbi-gh404-baseline.txt')
if baseline.exists():
    frame=baseline.read_text()
    # Capture is plain screen text from the owned 160x45 fixture.
    (ROOT/'baseline-160x45.txt').write_text(frame)
    body += '<h2>Current terminal baseline</h2><p>This captured fixture frame anchors the existing topology. The native concepts above intentionally use different sample tickets and omit fixture-only Focus groups.</p><figure><pre class="base">'+escape(frame)+'</pre><figcaption>Current Kanbi · disposable Master fixture · 160 by 45 cells · no production data.</figcaption></figure>'
body=body.replace('href="../../herdr-app-investigation-gh404.md"','href="../../herdr-app-investigation-gh404.md"')
(ROOT/'proposal.html').write_text('<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kanbi in the Herdr app proposal</title><style>'+STYLE+'</style></head><body><main><p class="overview">Product direction / GH-404</p>'+body+'</main></body></html>')
print(ROOT/'proposal.html')
