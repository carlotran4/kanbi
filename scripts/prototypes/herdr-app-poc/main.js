import { View, div } from "gpui-kit";
import { v_flex, h_flex, InputState, Input } from "gpui-base";
import { exec, home, theme, nextCommand } from "herdr";

export default class KanbiPOC extends View {
  init(_props,cx) {
    this.data={tickets:[],terminals:[],panes:{}};
    this.selected=null; this.detail=null; this.error=""; this.message=""; this.busy=false; this.loading=false;
    this.title=InputState.new({placeholder:"New ticket title"});
    this.note=InputState.new({placeholder:"Add a note to the selected ticket"});
    this.title.on("submit",(_e,cx)=>this.action("add",{title:this.title.value()},cx));
    this.handle=cx.focus_handle();
    this.handle.focus();

    cx.spawn(async cx=>{for(;;){
      const command=await nextCommand();
      if(command==="focus"){this.handle.focus();cx.notify();}
      if(command==="test-create"){this.title.set_value("UI verified ticket");this.action("add",{title:this.title.value()},cx);}
      if(command==="select-codex"){this.selected=this.data.tickets.filter(t=>t.harness==="codex").at(-1)?.display_id||this.selected;cx.notify();}
      if(command==="review"&&this.selected)this.action("move",{ticket:this.selected,column:"Review"},cx);
      if(command==="open"&&this.selected)this.action("open",{ticket:this.selected},cx);
    }});
    this.refresh(cx);
    cx.timer.every(3000,cx=>this.refresh(cx));
  }
  async call(action,params={}) {
    const result=await exec("/usr/bin/python3",[home()+"/.local/share/kanbi/herdr-app-poc/bin/ui.py",action,JSON.stringify(params)],{timeout:60000});
    let data; try{data=JSON.parse(result.stdout);}catch(_){throw new Error(result.stderr||result.stdout||"No response");}
    if(result.code!==0||data.error) throw new Error(data.error||result.stderr||"Action failed");
    return data;
  }
  refresh(cx) {
    if(this.loading||this.busy)return;
    this.loading=true;
    cx.spawn(async cx=>{
      try{this.data=await this.call("snapshot"); this.error=""; if(!this.selected&&this.data.tickets.length)this.selected=this.data.tickets[0].display_id;}
      catch(e){this.error=String(e);}
      finally{this.loading=false;cx.notify();}
    });
  }
  action(name,params,cx) {
    if(this.busy)return;
    if(name==="add"&&!params.title.trim())return;
    if(name==="note"&&!params.body.trim())return;
    this.busy=true;this.message="Working…";cx.notify();
    cx.spawn(async cx=>{
      try{
        const result=await this.call(name,params);
        if(name==="show")this.detail=result.ticket;
        if(name==="add")this.title.set_value("");
        if(name==="note")this.note.set_value("");
        this.data=await this.call("snapshot");if(result.ticket?.display_id&&(name==="add"||name==="codex"))this.selected=result.ticket.display_id;this.error="";this.message=name==="open"?"Existing attempt focused or a new agent started.":"Saved.";
      }catch(e){this.error=String(e);this.message="";}
      finally{this.busy=false;cx.notify();}
    });
  }
  button(id,label,click,t,primary=false) {
    return div().id(id).px(12).py(8).rounded(6).bg(primary?t.accent:t.raised).text_color(primary?t.background:t.foreground).text_size(12).cursor_pointer().hover(s=>s.opacity(.85)).on_click(click).child(label);
  }
  runtime(ticket) {
    const name=ticket.runtime||ticket.state||"not started";
    const matches=Object.values(this.data.panes).filter(p=>p.label?.includes(ticket.display_id));
    const terminal=matches.map(p=>this.data.terminals.find(t=>t.address===p.terminal&&t.labels?.["kanbi.poc.pane"]===p.pane_id)).filter(Boolean).reverse().find(t=>t.state==="live");
    return terminal ? (terminal.status?.state||"live · status unknown") : name;
  }
  key(e,cx) {
    const key=e.keystroke;
    if(key==="ctrl-alt-n"){this.title.focus();cx.stop_propagation();}
    if(key==="ctrl-alt-r"&&this.selected){this.action("move",{ticket:this.selected,column:"Review"},cx);cx.stop_propagation();}
    if(key==="ctrl-alt-o"&&this.selected){this.action("open",{ticket:this.selected},cx);cx.stop_propagation();}
    if(key==="ctrl-alt-h"&&this.selected){this.action("show",{ticket:this.selected},cx);cx.stop_propagation();}
    if(key==="ctrl-alt-j"){
      const ts=this.data.tickets; const i=ts.findIndex(a=>a.display_id===this.selected);
      if(ts.length)this.selected=ts[(i+1)%ts.length].display_id;
      this.detail=null;cx.notify();cx.stop_propagation();
    }
  }
  render(cx) {
    const t=theme(); const selected=this.data.tickets.find(a=>a.display_id===this.selected);
    const root=v_flex().id("kanbi-poc-root").track_focus(this.handle).on_key_down((e,cx)=>this.key(e,cx)).size_full().min_w(0).bg(t.canvas).text_color(t.foreground).p(18).gap(12);
    root.child(h_flex().gap(12).items_center().child(div().flex_1().text_size(22).font_semibold().child("Kanbi / App Sandbox"))
      .child(this.button("full-tui","Full TUI",(_e,cx)=>this.action("tui",{},cx),t))
      .child(this.button("expand-board","Expand board",(_e,cx)=>this.action("board",{},cx),t)));
    root.child(div().text_size(12).text_color(t.muted).child("PROOF OF CONCEPT · private database · Demo is offline · Codex uses your account"));
    root.child(h_flex().gap(8).child(Input.new(this.title).flex_1().h(36).px(10).rounded(6).border_1().border_color(t.border).bg(t.input))
      .child(this.button("create-ticket","Create demo ticket",(_e,cx)=>this.action("add",{title:this.title.value()},cx),t,true))
      .child(this.button("create-codex","New Codex ticket",(_e,cx)=>this.action("codex",{},cx),t)));
    root.child(div().text_size(12).text_color(this.error?"#e8a2a2":t.muted).child(this.error||this.message||"Select a ticket, then Open demo. Type work or permission in its live terminal."));
    root.child(h_flex().id("kanbi-columns").flex_1().min_h(160).overflow_x_scroll().items_start().gap(10).children(["Open","In Progress","Review","Done"].map(column=>{
      const tickets=this.data.tickets.filter(a=>(a.column?.name||a.column_name)===column);
      return v_flex().id("column-"+column).w(220).h_full().flex_none().gap(10).child(div().text_size(13).font_semibold().pb(8).border_b_1().border_color(t.border).child(column.toUpperCase()+"  "+tickets.length))
        .child(v_flex().id("cards-"+column).flex_1().overflow_y_scroll().gap(9).children(tickets.map(ticket=>{
          const active=ticket.display_id===this.selected; const state=this.runtime(ticket); const attention=state==="blocked"||state==="needs_permission";
          return v_flex().id("ticket-"+ticket.display_id).p(12).gap(9).bg(active?t.selected:t.surface).rounded(7).border_l_4().border_color(attention?"#e8c06b":active?t.accent:t.border).cursor_pointer()
            .on_click((_e,cx)=>{this.selected=ticket.display_id;this.detail=null;cx.notify();})
            .child(div().text_size(11).text_color(attention?"#e8c06b":t.muted).child(ticket.display_id+" · "+state))
            .child(div().text_size(14).font_medium().child(ticket.title))
            .child(div().text_size(11).text_color(t.dim).child((ticket.harness==="codex"?"Real Codex":"Offline demo")+" · App Sandbox"));
        })));
    })));
    if(selected){
      root.child(v_flex().gap(9).p(12).rounded(7).bg(t.surface)
        .child(div().font_semibold().child(selected.display_id+" · "+selected.title))
        .child(div().text_size(12).text_color(t.muted).child(selected.body||"No description"))
        .child(h_flex().gap(8).flex_wrap()
          .child(this.button("open-demo",selected.harness==="codex"?"Open Codex":"Open demo",(_e,cx)=>this.action("open",{ticket:this.selected},cx),t,true))
          .child(this.button("move-progress","In progress",(_e,cx)=>this.action("move",{ticket:this.selected,column:"In Progress"},cx),t))
          .child(this.button("move-review","Move to review",(_e,cx)=>this.action("move",{ticket:this.selected,column:"Review"},cx),t))
          .child(this.button("move-done","Done",(_e,cx)=>this.action("move",{ticket:this.selected,column:"Done"},cx),t))
          .child(this.button("ticket-history","Details",(_e,cx)=>this.action("show",{ticket:this.selected},cx),t)))
        .child(h_flex().gap(8).child(Input.new(this.note).h(34).flex_1().px(8).bg(t.input).rounded(5))
          .child(this.button("save-note","Save note",(_e,cx)=>this.action("note",{ticket:this.selected,body:this.note.value()},cx),t)))
        .when(Boolean(this.detail),el=>el.child(v_flex().id("ticket-details").max_h(160).overflow_y_scroll().gap(5)
          .child(div().text_size(11).text_color(t.muted).child(this.detail?.session ? "Attempt "+this.detail.session.id+" · "+this.detail.session.status+" · "+(this.detail.session.active?"active":"inactive")+" · resume not verified" : "No session yet"))
          .children((this.detail?.notes||[]).map(n=>div().text_size(12).child(n.body))))));
    }
    root.child(div().text_size(11).text_color(t.dim).child(this.busy?"Action in progress — please wait":"Ctrl+Alt+N new · J next · O open · R review · H details. Full TUI for session settings."));
    return root;
  }
}
