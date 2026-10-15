(async () => {
 const wait=()=>new Promise(r=>setTimeout(r,140)),check=(value,message)=>{if(!value)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const go=async path=>{location.hash=path;await wait()};
 const click=async(label,root=document)=>{const button=[...root.querySelectorAll('button')].find(item=>item.textContent.trim()===label||item.getAttribute('aria-label')===label);check(button,'Missing button '+label);check(!button.disabled,'Disabled '+label);button.click();await wait()};
 const fill=async(label,value)=>{const input=document.querySelector(`dialog [name="${label}"]`);check(input,'Missing field '+label);Object.getOwnPropertyDescriptor(input.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const choose=async(label,text)=>{const select=document.querySelector(`[role=combobox][aria-label="${label}"]`);check(select,'Missing select '+label);select.click();await wait();const option=[...document.querySelectorAll('[role=option]')].find(item=>typeof text==='function'?text(item.textContent.trim()):item.textContent.trim()===text);check(option,'Missing option '+text);option.click();await wait()};
 const year=new Date().getFullYear(),date=`${year}-01-01`,observationDate=new Date(Date.now()-60000);observationDate.setMinutes(observationDate.getMinutes()-observationDate.getTimezoneOffset());
 const observed=observationDate.toISOString().slice(0,16);
 await go('/goals');await click('新建目标');await fill('目标名称','浏览器业务结果');await choose('目标归属',text=>text.startsWith('租户 · '));await choose('目标周期','年度');await fill('周期内日期',date);await fill('目标时区','UTC');
 await fill('受益对象','依赖固定交付成果的项目成员');await fill('期望改变与下游价值','缩短固定成果交付周期，同时提升可核对的有效成果数量');await fill('资源与实现假设','由现有项目成员执行，Verifier依据固定材料提供本地审查模拟');
 const collaborator=[...document.querySelectorAll('dialog fieldset > div')].find(row=>row.querySelector('b')?.textContent==='Verifier');check(collaborator,'Verifier collaboration controls missing');const draftPermission=[...collaborator.querySelectorAll('label')].find(label=>label.textContent.trim()==='草稿与价值审查')?.querySelector('input');check(draftPermission,'Verifier draft review permission missing');draftPermission.click();await wait();
 await fill('KR 1 标题','交付周期改善');await fill('KR 1 单位','天');await choose('KR 1 方向','降低');await fill('KR 1 基线','10');await fill('KR 1 目标值','nonsense');await fill('KR 1 权重','1');await click('保存目标草稿',document.querySelector('dialog'));
 check(document.querySelector('dialog [role=alert]')?.textContent.includes('有限数值'),'Invalid numeric target not rejected');
 await fill('KR 1 目标值','5');await click('添加 KR');await fill('KR 2 标题','有效成果增长');await fill('KR 2 单位','项');await fill('KR 2 基线','0');await fill('KR 2 目标值','100');await fill('KR 2 权重','3');await click('保存目标草稿',document.querySelector('dialog'));
 check(!document.querySelector('dialog'),'Goal did not save');const goalId=location.hash.split('/').at(-1),goal=()=>state().resources.goals.find(item=>item.id===goalId);
 check(goal().krs.length===2&&goal().status==='草稿','Typed objective not created');
 const observe=async(index,value)=>{await click(`记录观察 · KR ${index}`);await fill('观察值',value);await fill('观察时间',observed);await fill('观察依据','按本周期固定经营记录核对');document.querySelector('dialog fieldset input[type=checkbox]').click();await wait();await click('保存观察',document.querySelector('dialog'));check(!document.querySelector('dialog'),'Observation not saved')};
 await observe(1,'7.5');check(document.querySelector('main').textContent.includes('数据不完整'),'Missing KR falsely became complete');await observe(2,'100');check(document.querySelector('main').textContent.includes('87.5%'),'Weighted progress not explained');
 await click('记录人工进度判断');await fill('人工判断进度（%）','100');await fill('依据与说明','人工判断不替代公式');await click('确认操作',document.querySelector('dialog'));check(document.querySelector('main').textContent.includes('87.5%'),'Manual assessment overrode actual');
 // Previous full-suite failure: Disabled 提交审核. Preserve the actual gate,
 // then record the authorized, explicitly local Agent review required by this definition.
 const submitBeforeReview=[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='提交审核');check(submitBeforeReview?.disabled&&submitBeforeReview.title.includes('价值审查'),'Unreviewed definition bypassed the semantic review gate');
 await click('记录本地 Agent 价值审查');await choose('审查 Agent','Verifier');
 for(const [label,basis] of [
  ['合理性','本地模拟：交付周期与有效成果分别有明确基线、目标和当前观察。'],
  ['可实现性','本地模拟：当前观察为7.5天与100项，按原有成员范围核对改善目标。'],
  ['有效性','本地模拟：核对交付周期下降及有效成果增加，不把任务数量替代业务结果。'],
  ['项目 / 下游贡献','本地模拟：目标为依赖固定成果的项目成员提供更及时、可核对的交付。'],
  ['重复投入','本地模拟：两个KR衡量不同结果，未将对齐关系重复计入自身进度。'],
  ['资源匹配','本地模拟：采用已声明项目成员和获草稿授权的Verifier，不扩展执行权限。'],
 ]){await choose(label+'结论','本地模拟通过');await fill(label+'逐项依据',basis);}
 await click('保存记录',document.querySelector('dialog'));check(!document.querySelector('dialog'),'Value review did not save');check(goal().valueReview?.source==='LOCAL_SIMULATION'&&goal().valueReview.agentId==='verifier'&&Object.values(goal().valueReview.checks).every(item=>item.result==='PASS'&&item.basis),'Explicit authorized value review missing');
 const action=async label=>{await click(label);await fill('依据与说明','核对当前固定修订');await click('确认操作',document.querySelector('dialog'));check(!document.querySelector('dialog'),label+' not saved')};await action('提交审核');await action('批准目标');
 check(document.querySelector('a[href*="artifacts"][href*="version"]'),'Fixed work evidence route missing');const evidenceDetails=[...document.querySelectorAll('details')].find(item=>item.querySelector('summary')?.textContent==='查看保存的证据快照');check(evidenceDetails,'Saved evidence snapshot missing');evidenceDetails.open=true;check(evidenceDetails.querySelector('pre').textContent.includes('version'),'Snapshot not rendered');
 await observe(1,'5');check(goal().approval&&goal().status==='进行中','Observation incorrectly removed approved definition');
 await click('检查目标风险');const count=goal().reminders.length;await click('检查目标风险');check(goal().reminders.length===count,'Risk check duplicated reminders');
 await go('/goals');await click('新建目标');await fill('目标名称','对齐与自动数据');await choose('目标归属',text=>text.startsWith('租户 · '));await choose('目标周期','年度');await fill('周期内日期',date);await fill('目标时区','UTC');await fill('KR 1 标题','自动来源');await fill('KR 1 单位','%');await fill('KR 1 基线','0');await fill('KR 1 目标值','100');await choose('KR 1 来源','自动事实');await choose('KR 1 自动指标','首次验收率');
 await click('添加对齐');await choose('对齐 1 目标','浏览器业务结果');await choose('对齐 1 KR','交付周期改善');await choose('对齐 1 关系','跨目标贡献');await fill('对齐 1 依据','本目标贡献于上级结果');await click('添加对齐');await click('移除对齐 2');await click('保存目标草稿',document.querySelector('dialog'));const autoId=location.hash.split('/').at(-1);check(state().resources.goals.find(item=>item.id===autoId).alignments.length===1,'Alignment controls failed');await click('刷新自动指标');check(document.querySelector('main').textContent.includes('数据不完整')&&state().resources.goals.find(item=>item.id===autoId).krs[0].observations.at(-1).value===null,'Unavailable AUTO data fabricated zero');await go('/goals/'+goalId);
 await go('/goals');await click('新建目标');
 const guidance=[...document.querySelectorAll('dialog details')].find(item=>item.querySelector('summary')?.textContent==='如何添加有效的 O / KR');check(guidance,'Initial O/KR guidance missing');guidance.open=true;await wait();
 for(const text of ['正例','反例','过程指标与保障工作','不是 Agent 语义审查结论'])check(guidance.textContent.includes(text),'Guidance missing '+text);
 check(!document.querySelector('dialog [name="本次调整理由"]'),'New goal demands fabricated change reason');
 await fill('目标名称','项目周目标');await choose('目标归属',text=>text.startsWith('项目 · '));await fill('项目阶段名称','跨年周交付阶段');await choose('目标周期','周度');await fill('周期内日期','2027-01-01');await fill('目标时区','UTC');await fill('KR 1 标题','下游可用指引');await fill('KR 1 单位','%');await fill('KR 1 基线','70');await fill('KR 1 目标值','90');await click('保存目标草稿',document.querySelector('dialog'));
 check(!document.querySelector('dialog'),'Project weekly goal not saved');const weeklyId=location.hash.split('/').at(-1),weekly=()=>state().resources.goals.find(item=>item.id===weeklyId);
 check(weekly().periodKind==='WEEK'&&weekly().periodStart==='2026-12-28'&&weekly().periodEnd==='2027-01-03'&&weekly().history.length===0,'Weekly boundaries or empty creation history wrong');
 const oldWeekly=structuredClone(weekly());await click('编辑目标定义');check(document.querySelector('[name="本次调整理由"]').value==='','Reason was prefilled');await fill('目标名称','请假后的项目周目标');await fill('本次调整理由','   ');
 const beforeRejected=localStorage.getItem('collaboration-prototype-v1');await click('保存目标草稿',document.querySelector('dialog'));check(document.querySelector('dialog [role=alert]')?.textContent.includes('本次调整理由'),'Blank reason not rejected');check(localStorage.getItem('collaboration-prototype-v1')===beforeRejected,'Rejected save changed persisted state');
 await fill('本次调整理由','本周请假两天，下游使用时间保持不变并收缩工作范围');await click('保存目标草稿',document.querySelector('dialog'));check(!document.querySelector('dialog'),'Reasoned edit not saved');
 const change=weekly().history.at(-1);check(change.before.name===oldWeekly.name&&change.after.name==='请假后的项目周目标'&&change.actor.id==='H-lin'&&change.time&&change.reason.includes('请假'),'Fixed change record incomplete');
 check(document.querySelector('main').textContent.includes(change.reason),'Change reason not visible in history');
 await click('编辑目标定义');check(document.querySelector('[name="本次调整理由"]').value==='','Previous reason automatically reused');await click('关闭弹窗');
 await go('/goals/'+goalId);
 await click('提交期末自评');await fill('期末自评','本期指标达到，业务结果已观察');await fill('依据与说明','两项当前观察满足目标');await click('确认操作',document.querySelector('dialog'));check(goal().review?.status==='待审核','Review not submitted');
 await click('确认期末结果');await choose('期末结果','已达成');await fill('依据与说明','Human复核观察及数值');await click('确认操作',document.querySelector('dialog'));check(goal().status==='已达成','Goal not closed');
 await click('结转为下期草稿');await fill('下期内日期',`${year+1}-01-01`);await fill('依据与说明','下期重新确认指标');await click('确认操作',document.querySelector('dialog'));
 const next=state().resources.goals.find(item=>item.id===location.hash.split('/').at(-1));check(next.carriedFrom.goalId===goalId&&next.krs.every(kr=>kr.baseline===null&&kr.observations.length===0),'Carryover fabricated values/evidence');
 await go('/goals/G-01');await click('转换为 Objective / KR');check(document.querySelector('[name="KR 1 基线"]').value==='8 天','Legacy value silently coerced');
 await choose('目标归属',text=>text.startsWith('租户 · '));await choose('目标周期','年度');await fill('周期内日期',date);await fill('目标时区','UTC');await fill('KR 1 单位','天');await choose('KR 1 方向','降低');await fill('KR 1 基线','8');await fill('KR 1 目标值','5');await fill('本次调整理由','   ');
 const conversionBefore=localStorage.getItem('collaboration-prototype-v1');await click('保存目标草稿',document.querySelector('dialog'));check(document.querySelector('dialog [role=alert]')?.textContent.includes('本次调整理由'),'Legacy conversion accepted empty reason');check(localStorage.getItem('collaboration-prototype-v1')===conversionBefore,'Rejected conversion changed state');
 await fill('本次调整理由','把历史天数字符串转换为明确的基线、目标和观察口径');await click('保存目标草稿',document.querySelector('dialog'));check(!document.querySelector('dialog'),'Reasoned legacy conversion failed');
 const converted=state().resources.goals.find(item=>item.id==='G-01');check(converted.history.at(-1).before.baseline==='8 天'&&converted.history.at(-1).after.krs[0].baseline===8,'Conversion before/after missing');await click('编辑目标定义');check(document.querySelector('[name="本次调整理由"]').value==='','Conversion reason reused on new edit');
 check(document.documentElement.scrollWidth<=innerWidth+1,'Goal editor overflows viewport');
 return {pass:true,checks:['typed invalid input rejected','two KR weighted formula','missing data unknown','manual assessment separate','fixed submit/approve','observation retains definition approval','risk dedupe','period self-review/Human close','carryover empty facts','legacy explicit conversion','project weekly calendar boundary','built-in purpose and support-work guidance','reason rejection atomicity','fixed before-after actor and history','reason resets after edit and conversion'],width:innerWidth};
})()
