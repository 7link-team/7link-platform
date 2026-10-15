import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "bun:test";
import { ReportContentReader, ReportContentEditor } from "../src/report-content-ui";
import { emptyReportContent, reportDetailFingerprint, reportRetroFingerprint } from "../src/report-content";
import { read } from "../src/store";
const report = { subjectKey:"human:H-lin",authorId:"H-lin",authorKind:"Human",cadence:"日报",periodStart:"2026-10-14",periodEnd:"2026-10-14",timezone:"Asia/Singapore" };
const editor = content => renderToStaticMarkup(<ReportContentEditor state={read()} form={{...report,content}} setForm={()=>{}} />);
const reader = (content,state=read()) => renderToStaticMarkup(<ReportContentReader content={content} report={report} state={state} />);
test("corrupt adopted Plan does not crash structured or legacy report editors",()=>{
 for(const binding of [{},{snapshot:null},{snapshot:{}},{snapshot:{items:{}}},{snapshot:{items:[null]}},{snapshot:{items:[{}]}},{snapshot:{items:[{id:"item",title:null}]}}, {revision:{bad:true},snapshot:{items:[]}}, {planId:{bad:true},snapshot:{items:[]}}, {status:{bad:true},snapshot:{items:[]}}, {snapshot:{periodStart:{bad:true},items:[]}}, ...["goalId","krId","dueDate","workId","freeCategory"].map(key=>({snapshot:{items:[{id:"item",title:"标题",[key]:{bad:true}}]}}))]){
  const original=structuredClone(binding);
  for(const content of [null,emptyReportContent()]){
   const form={...report,content,nextPlanBinding:binding};
   expect(renderToStaticMarkup(<ReportContentEditor state={read()} form={form} setForm={()=>{throw Error("unexpected mutation")}} />)).toContain("固定下期计划快照损坏");
  }
  expect(binding).toEqual(original);
 }
});
test("malformed stored and recovered report sections render explicit errors without replacing raw data",()=>{
 const basic=emptyReportContent();
 for(const content of [{},{...basic,summary:null},{...basic,completed:[null]},{...basic,retrospective:{problems:[]}},{...basic,retrospective:{...basic.retrospective,improvements:[null]}}]){
  const before=structuredClone(content);
  for(const render of [editor,reader])expect(render(content)).toContain("原始内容仍保留");
  expect(content).toEqual(before);
 }
});
test("confirmation controls verify author as well as fingerprint; historical authors survive roster deletion",()=>{
 const basic=emptyReportContent(),item={key:"pending",title:"待办事项",reason:"OTHER",detail:"检查资料仍待补齐",carry_over:"CARRY_TO_NEXT"};
 const retro={went_well:["沟通顺畅"],problems:["资料缺失"],root_causes:["交接未完成"],improvements:[{text:"完善交接检查",propose_memory:false}],origin:"AI",ai_drafted:false};
 const stamp=(fingerprint,actorId)=>({fingerprint,actorId,actorKind:"Human",recordedBy:"H-lin"});
 for(const actorId of ["H-zhou","H-lin"]){
  const content={...basic,uncompleted:[{...item,detail_confirmation:stamp(reportDetailFingerprint(item),actorId)}],retrospective:{...retro,confirmation:stamp(reportRetroFingerprint(retro),actorId)}};
  const html=editor(content),inputs=(html.match(/<input[^>]+type="checkbox"[^>]*>/g)||[]).filter(input=>/aria-label="确认(?:原因与处置|复盘内容)/.test(input));expect(inputs).toHaveLength(2);
  for(const input of inputs)expect(input.includes('checked=""')).toBe(actorId==="H-lin");
  if(actorId==="H-zhou"){expect(reader(content)).toContain("作者尚未确认");expect(reader(content)).toContain("尚未经作者确认");}
  else {const missing=structuredClone(read());missing.organization.members=missing.organization.members.filter(person=>person.id!=="H-lin");expect(reader(content,missing)).toContain("作者确认：Human H-lin");}
 }
});

test("Plan source labels retain unavailable IDs and selectors use genuine matching fixed items",()=>{
 const state=read(), items=[{id:"fixed-work",title:"固定工作",kind:"WORK_ITEM",workId:"W-103",weight:1},{id:"fixed-free",title:"固定调研",kind:"FREE_TASK",freeCategory:"LEARNING",criteria:"提供结论",weight:1}];
 const binding={planId:"fixed-plan",tenantId:state.activeTenant,sourceRevision:1,revision:1,status:"草稿",confirmed:false,snapshot:{...report,periodKind:"DAY",revision:1,items}};
 const content={...emptyReportContent(),completed:[{key:"done",title:"已接受工作",fact_ref:{kind:"WORK_ITEM",id:"W-103",event_id:"accepted",version:1},plan_item_id:"fixed-work"}],uncompleted:[{key:"pending",title:"未完成调研",plan_item_id:"fixed-free",reason:"OTHER",detail:"资料尚未齐备",carry_over:"CARRY_TO_NEXT"}]};
 const form={...report,currentPlanBinding:binding,content},before=structuredClone(form);
 const html=renderToStaticMarkup(<ReportContentEditor state={state} form={form} setForm={()=>{throw Error("unexpected write")}} />);
 expect(html).toContain('aria-label="完成计划关联 1"');expect(html).toContain('aria-label="未完成计划关联 1"');expect(html).toContain("固定工作 · fixed-work");expect(html).toContain("固定调研 · fixed-free");
 const body=renderToStaticMarkup(<ReportContentReader state={state} report={form} content={content} />);expect(body).toContain("固定计划项");expect(body).toContain("fixed-work");expect(body).toContain("固定调研");expect(form).toEqual(before);
 const broken={...form,currentPlanBinding:{...binding,snapshot:{...binding.snapshot,items:[null]}}};
 const unavailable=renderToStaticMarkup(<ReportContentEditor state={state} form={broken} setForm={()=>{}} />);expect(unavailable).toContain("原引用无法核对 · fixed-free");expect(unavailable).toContain("已确认计划项");
});

test("operation evidence requires the complete typed ID and version in both reader and editor",()=>{
 const state=structuredClone(read()),time=new Date().toISOString(),day=new Intl.DateTimeFormat("en-CA",{timeZone:"Asia/Singapore",year:"numeric",month:"2-digit",day:"2-digit"}).format(new Date(time));
 const fact={id:"SEND-typed",kind:"DELIVERY",tenantId:state.activeTenant,project:"launch",deliveryId:"D-typed",workId:"W-103",version:2,status:"SENT",actor:{kind:"Human",id:"H-lin"},time,subjectKeys:[`tenant:${state.activeTenant}`,"project:launch","human:H-lin"],source:"LOCAL_SIMULATION",deliverySnapshot:{id:"D-typed",workId:"W-103",version:2,project:"launch",name:"固定交付测试",recipient:"下游接收人",content:"固定包引用"}};
 state.resources.deliveries.push({id:fact.deliveryId,status:"待回执",sendFact:fact});
 for(const patch of [{},{id:"OTHER-DELIVERY"},{version:999},{event_id:"OTHER-SEND"}]){
  const content={...emptyReportContent(),completed:[{key:"typed",title:"固定发送事实",plan_item_id:"",fact_ref:{kind:"DELIVERY",id:fact.deliveryId,event_id:fact.id,version:2,...patch},note:""}]};const form={...report,periodStart:day,periodEnd:day,content},before=structuredClone(form);
  for(const kind of ["reader","editor"]){const html=renderToStaticMarkup(kind==="reader"?<ReportContentReader state={state} report={form} content={content}/>:<ReportContentEditor state={state} form={form} setForm={()=>{throw Error("unexpected write")}}/>);
   // Picker may still show the valid available fact; inspect only the selected
   // completed item in the editor, never conflate candidates with adopted facts.
   const selected=kind==="editor"?html.slice(html.indexOf('<legend>完成条目 1</legend>'),html.indexOf('移除完成条目 1')):html;
   if(!Object.keys(patch).length){expect(selected).toContain("本地模拟 SENT 回执");expect(selected).toContain("H-lin");}
   else {expect(selected).toContain("裁决/交付证据暂时无法核对");expect(selected).not.toContain("本地模拟 SENT 回执");}
  }expect(form).toEqual(before);
 }
});

test("chapter refill controls are explicitly labeled and start with no sections selected",()=>{
 const html=editor(emptyReportContent()),section=html.slice(html.indexOf('<details data-report-regeneration'),html.indexOf('</details>',html.indexOf('<details data-report-regeneration')));
 expect(section).toContain("保留未选章节");expect(section).toContain("只修改当前表单");
 const controls=section.match(/<input[^>]+type="checkbox"[^>]*>/g)||[];expect(controls).toHaveLength(3);
 for(const name of ["completed","uncompleted","next_plan"])expect(controls.some(input=>input.includes(`name="regenerate-${name}"`))).toBe(true);
 for(const input of controls)expect(input).not.toContain('checked=""');expect(section).toContain("重填所选章节");
});
