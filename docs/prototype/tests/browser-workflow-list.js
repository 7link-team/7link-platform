(async()=>{
 const checks=[],check=(ok,name)=>{checks.push({name,pass:Boolean(ok)});if(!ok)throw Error(name);},wait=()=>new Promise(resolve=>setTimeout(resolve,220));
 const click=async(label,root=document)=>{const el=[...root.querySelectorAll('button,a')].find(el=>(el.textContent.trim()===label||el.getAttribute('aria-label')===label)&&!el.disabled);check(el,'Control '+label);el.click();await wait();};
 const fill=async(label,value)=>{const el=document.querySelector(`[aria-label="${label}"]`)||document.querySelector(`[name="${label}"]`);check(el,'Field '+label);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait();};
 const select=async(label,option)=>{const el=document.querySelector(`[role="combobox"][aria-label="${label}"]`);check(el,'Select '+label);el.click();await wait();await click(option,document.querySelector('[role="listbox"]'));};
 const list=()=>JSON.parse(new URLSearchParams(location.hash.split('?')[1]).get('state.workflow-list')||'{}');
 const rows=()=>[...document.querySelectorAll('.resource-data-list tbody tr')];
 location.hash='/p/launch/flow';await wait();
 check(rows().length===20&&document.querySelector('.resource-data-list').textContent.includes('匹配 24 条'),'Pagination uses all authorized active definitions');
 await click('下一页');check(rows().length===4&&rows()[0].cells[0].textContent==='1'&&list().page===2,'Page URL advances while page-local sequence restarts at 1');
 await fill('搜索当前授权列表','LIST_FLOW_01');check(rows().length===1&&rows()[0].textContent.includes('LIST_FLOW_01')&&list().page===1,'Search covers records outside the former page and resets page');
 await fill('搜索当前授权列表','NO_MATCH');check(document.querySelector('.resource-data-list').textContent.includes('没有匹配'),'Zero results has an explicit empty state');await click('清空条件');
 await select('每页条数','10 条');check(rows().length===10&&list().pageSize===10,'Custom page size persists URL');
 await click('按定义排序，当前升序');check(list().sort==='definition'&&list().order==='desc'&&document.querySelector('th[aria-sort="descending"]'),'Sort direction is semantic and URL-backed');
 const region=document.querySelector('.resource-data-list [role="region"]');region.scrollTop=150;region.scrollLeft=160;region.dispatchEvent(new Event('scroll',{bubbles:true}));await wait();
 const before=JSON.stringify(list()),position={top:region.scrollTop,left:region.scrollLeft};await click('刷新');check(JSON.stringify(list())===before&&region.scrollTop===position.top&&region.scrollLeft===position.left&&document.querySelector('.resource-data-list').textContent.includes('最近成功：')&&!document.querySelector('.resource-data-list').textContent.includes('最近成功：尚未刷新'),'Refresh retains query and scroll and reports a real local read');
 const saved=location.hash;location.hash='/overview';await wait();location.hash=saved;await wait();const restored=document.querySelector('.resource-data-list [role="region"]');check(list().pageSize===10&&list().order==='desc'&&restored.scrollTop===position.top&&restored.scrollLeft===position.left,'Precise list URL restores sorting page size and both scroll axes');
 const original=URL.createObjectURL;let downloaded;URL.createObjectURL=blob=>{downloaded=blob;return original(blob);};
 try{await click('导出当前查询结果');}finally{URL.createObjectURL=original;}
 check(downloaded instanceof Blob,'Export creates an actual JSON blob');const payload=JSON.parse(await downloaded.text());
 check(payload.count===24&&payload.records.length===24&&payload.records.every(r=>r.definition.projectId==='launch'&&Number.isInteger(r.version)),'Export covers query rather than current page and includes exact scope and revision');
 check(!JSON.stringify(payload).includes('DO_NOT_EXPORT_NODE_CONFIGURATION')&&payload.records.filter(r=>r.id!=='default').every(r=>r.definition.publishedVersion===null),'Export excludes node configuration and does not invent publication');
 check(getComputedStyle(restored.querySelector('th')).position==='sticky'&&getComputedStyle(restored.querySelector('tbody tr td:first-child')).position==='sticky'&&getComputedStyle(restored.querySelector('tbody tr td:last-child')).position==='sticky','Table header local sequence and operation column are sticky');
 await click('更多操作',rows()[0]);check(document.querySelector('dialog[open]')&&new URLSearchParams(location.hash.split('?')[1]).has('state.workflow-list-menu'),'Row operations open a precise modal instead of another route');await click('关闭弹窗',document.querySelector('dialog[open]'));
 await click('已归档');check(rows().length===2&&list().filter==='archived'&&list().page===1,'Archived filter preserves records and resets pagination');
 check(document.documentElement.scrollWidth<=innerWidth,'Workflow list fits viewport; overflow stays within table');
 return {pass:true,width:innerWidth,checks};
})()
