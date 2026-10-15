globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const api=await import('../src/store.js');const {completeValidation}=await import('./qa-helpers.js');const {workRework}=await import('../src/rework.js');
api.reset();let w=api.read().work.find(w=>w.id==='W-103');api.workCommand(w.id,'rework-config','测试场景明确Human配置返工上限',w.version,{revision:workRework(w).revision,internal:12,formal:12});
const cases=[];
for(const [index,status] of ['待回执','部分接受','已拒收','已接受'].entries()){
 api.update(s=>s.role='owner');w=api.read().work.find(w=>w.id==='W-103');
 if(index){if(w.status==='待交接')api.workCommand(w.id,'return','测试场景修订新包',w.version);w=api.read().work.find(w=>w.id==='W-103');api.workCommand(w.id,'submit','测试场景提交新版本',w.version);}
 completeValidation(api,w.id);w=api.read().work.find(w=>w.id==='W-103');api.workCommand(w.id,'accept','测试场景内部接受',w.version);api.workCommand(w.id,'deliver','测试场景固定交付',w.version);
 const d=api.read().resources.deliveries.find(d=>d.id===api.read().work.find(w=>w.id==='W-103').currentDeliveryId);
 if(status!=='待回执'){api.update(s=>s.role='external');api.resourceCommand('deliveries',d.id,status,'测试场景固定客户回执','',{version:d.version,items:d.items.map((item,i)=>({id:item.id,status:status==='部分接受'?(i===0?'已接受':'已拒收'):status,...(status==='已拒收'||status==='部分接受'&&i!==0?{feedback:{requirement:d.requirements[0],position:'检查记录第一条',expected:'保留完整可定位证据',actual:'当前检查证据缺少定位'}}:{})}))});}
 cases.push({id:d.id,status});
}
const s=api.read();s.testDeliveryReceiptCases=cases;console.log(JSON.stringify(s));
