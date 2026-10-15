(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve,180));
  const check = (value,message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const fixture = state().testPrivateKnowledge;
  check(fixture,"Missing genuine memory fixture");
  const go = async path => {location.hash=path;await wait();};
  const click = async (label,scope=document) => {const button=[...scope.querySelectorAll("button")].find(b=>b.textContent.trim()===label);check(button&&!button.disabled,"Missing enabled "+label);button.click();await wait();};
  const fill = async value => {const input=document.querySelector('dialog:modal textarea[name="依据与说明"]');check(input,"Missing reason");Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,"value").set.call(input,value);input.dispatchEvent(new Event("input",{bubbles:true}));await wait();};
  const record = () => state().resources.knowledge.find(r=>r.id===fixture.ownId);
  await go(`/knowledge/${fixture.ownId}`);
  check([...document.querySelectorAll("main button")].find(b=>b.textContent.trim()==="删除 / 撤销记忆")?.disabled,"Pending candidate falsely revocable");
  await click("确认 / 发布");await fill("本人核对派生经验内容有效");await click("确认操作",document.querySelector("dialog:modal"));
  check(record().status==="有效","Memory approval missing");
  const originalMessages=JSON.stringify(state().messages),originalContent=record().content;
  await click("删除 / 撤销记忆");check(document.querySelector("dialog:modal").textContent.includes("不可重新启用"),"Missing terminal confirmation");
  for(let attempt=0;attempt<2;attempt++) {
    await fill("短");await click("确认操作",document.querySelector("dialog:modal"));
    const error=document.querySelector('dialog:modal [role="alert"]'),bounds=error?.getBoundingClientRect(),dialog=document.querySelector("dialog:modal").getBoundingClientRect();
    check(error?.textContent.includes("5–2000")&&document.activeElement===error,"Rejected reason not announced/focused");
    check(bounds.top>=dialog.top&&bounds.bottom<=Math.min(innerHeight,dialog.bottom),"Error summary hidden");
    check(record().status==="有效"&&!record().revocation,"Refusal mutated original");
  }
  await fill("经验与当前事实冲突，不再用于后续执行");await click("确认操作",document.querySelector("dialog:modal"));
  check(!document.querySelector("dialog:modal")&&record().status==="已撤销"&&record().revocation?.actorId==="H-lin","Actual revocation missing");
  check(record().content===originalContent&&JSON.stringify(state().messages)===originalMessages,"Revocation erased original/messages");
  const detail=document.querySelector("main");
  check(detail.textContent.includes("记忆撤销记录")&&![...detail.querySelectorAll("button")].some(b=>["编辑","确认 / 发布","禁用记忆","删除 / 撤销记忆"].includes(b.textContent.trim())),"Historical detail retained mutable controls");
  const frozen=JSON.stringify(record());
  await go("/knowledge");check(!document.querySelector(`main a[href="#/knowledge/${fixture.ownId}"]`),"Revoked memory retained daily entry");
  const combo=document.querySelector('[role="combobox"][aria-label="资料范围"]');combo.click();await wait();[...document.querySelectorAll('[role="option"]')].find(option=>option.textContent.trim()==="我的私人资料").click();await wait();const history=[...document.querySelectorAll("main label")].find(label=>label.textContent.includes("包含归档 / 撤销历史"))?.querySelector("input");check(history,"Missing history selector");history.click();await wait();check(document.querySelector(`main a[href="#/knowledge/${fixture.ownId}"]`),"Revoked history lost its stable link");
  document.querySelector('[aria-label="全局搜索"]').click();await wait();const search=document.querySelector('[aria-label="全局搜索关键词"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value").set.call(search,fixture.ownName);search.dispatchEvent(new Event("input",{bubbles:true}));await wait();check(!document.querySelector("dialog:modal .search-results").textContent.includes(fixture.ownName),"Revoked memory remains default search context");
  document.querySelector('dialog:modal [aria-label="关闭弹窗"]').click();await wait();
  await go(`/knowledge/${fixture.ownId}`);
  const nativeURL=URL.createObjectURL,nativeClick=HTMLAnchorElement.prototype.click;let blob;
  try {URL.createObjectURL=function(value){blob=value;return nativeURL.call(URL,value);};HTMLAnchorElement.prototype.click=function(){};await click("导出");check(JSON.parse(await blob.text()).status==="已撤销","Historical export disguises revoked status");}
  finally {URL.createObjectURL=nativeURL;HTMLAnchorElement.prototype.click=nativeClick;}
  check(JSON.stringify(record())===frozen,"Read/history/search/export mutated revoked original");
  check(document.documentElement.scrollWidth<=innerWidth+2,"Memory lifecycle overflow");
  return {pass:true,width:innerWidth,checks:["actual own candidate confirmation","terminal revocation confirmation","repeated reason rejection visible focus","source/history retained","historical read-only stable identity","daily/default search exclusion","explicit historical list","actual historical Blob export","no read mutation"]};
})();
