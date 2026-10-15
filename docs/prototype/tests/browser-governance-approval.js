// Fresh owner-role session; UI-only fixture. State is read only for assertions.
(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,180)),check=(v,m)=>{if(!v)throw Error(m);},state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const button=(label,root=document)=>[...root.querySelectorAll('button')].find(b=>b.textContent.trim()===label);
 const click=async(label,root=document)=>{const b=button(label,root);check(b&&!b.disabled,'Missing enabled '+label);b.click();await wait();};
 const fill=async(label,value)=>{const e=document.querySelector(`[name="${label}"]`);check(e&&!e.disabled,'Missing '+label);Object.getOwnPropertyDescriptor(e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(e,value);e.dispatchEvent(new Event('input',{bubbles:true}));await wait();};
 const choose=async(label,text)=>{const e=document.querySelector(`[role=combobox][aria-label="${label}"]`);check(e,'Missing select '+label);e.click();await wait();const option=[...document.querySelectorAll('[role=option]')].find(o=>o.textContent.includes(text));check(option,'Missing option '+text);option.click();await wait();};
 location.hash='/models/MD-02';await wait();await click('提交当前配置审批');
 const request=state().resources.approvals.find(a=>a.target==='MD-02'&&a.status==='待审核');check(request,'Approval was not requested from source');
 const record=()=>state().resources.approvals.find(a=>a.id===request.id);
 const setup=[...document.querySelectorAll('summary')].find(e=>e.textContent.includes('设置多人审批'));if(setup&&!setup.parentElement.open){setup.click();await wait();}
 const participants=[...document.querySelectorAll('fieldset')].find(e=>e.querySelector('legend')?.textContent.includes('审批 Human'));check(participants,'Missing frozen participants editor');
 for(const name of ['林予','周宁']){const label=[...participants.querySelectorAll('label')].find(e=>e.textContent.includes(name));check(label,'Missing Human '+name);const input=label.querySelector('input');if(!input.checked)input.click();await wait();}
 await fill('审批操作依据','明确冻结当前两位 Human，全员通过后才能批准固定配置');await click('冻结多人审批规则');
 check(record().orchestration.participants.join(',')==='H-lin,H-zhou','Frozen roster/order missing');check(record().orchestration.strategy==='ALL','Unexpected approval strategy');
 check(button('批准当前版本')?.disabled,'Final approval bypasses zero votes');
 await choose('所代表的固定参与者','林予');await choose('本地模拟决定主体','林予');await fill('审批操作依据','第一位参与者核对固定版本');await click('记录参与者批准');
 check(record().orchestration.votes.length===1&&!record().decision,'First vote prematurely finalized');check(button('批准当前版本')?.disabled,'Final approval bypasses second required vote');
 await fill('审批操作依据','重复票必须拒绝');await click('记录参与者批准');check(record().orchestration.votes.length===1,'Duplicate vote appended');
 await choose('所代表的固定参与者','周宁');await choose('本地模拟决定主体','周宁');await fill('审批操作依据','第二位参与者本地模拟确认');await click('记录参与者批准');check(record().orchestration.votes.length===1,'Unmarked simulated actor accepted');
 const consent=[...document.querySelectorAll('label')].find(e=>e.textContent.includes('明确记录本地模拟'));check(consent,'Missing simulation consent');consent.querySelector('input').click();await wait();await click('记录参与者批准');
 check(record().orchestration.votes.length===2&&!record().decision,'Votes must feed original final command');check(record().orchestration.votes[1].recordedBy==='H-lin'&&record().orchestration.votes[1].actorId==='H-zhou','Actual recorder or simulated subject lost');
 await click('批准当前版本');await fill('依据与说明','已核对冻结名单与两票，批准固定配置');await click('确认操作',document.querySelector('dialog[open]'));
 check(record().status==='已批准'&&record().decision.targetRevision===request.targetRevision,'Original decision command did not finalize fixed version');
 check(document.documentElement.scrollWidth<=innerWidth,'Approval page overflows viewport');
 return {pass:true,width:innerWidth,checks:['source request','frozen two-Human roster','zero/one vote cannot finalize','duplicate vote rejected','other Human simulation requires consent','two votes retain real recorder','existing final decision command','no horizontal overflow']};
})();
