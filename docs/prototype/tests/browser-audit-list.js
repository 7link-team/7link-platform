(async()=>{
 const wait=()=>new Promise(resolve=>setTimeout(resolve,150)),check=(value,message)=>{if(!value)throw Error(message)};
 const click=async text=>{const el=[...document.querySelectorAll('button')].find(el=>el.textContent.trim()===text);check(el,'Missing '+text);el.click();await wait()};
 location.hash='/governance/audit';await wait();
 const rows=()=>[...document.querySelectorAll('.audit-list > a')];
 check(rows().length===50,'Audit does not bound rendered rows');
 check(document.documentElement.scrollWidth<=innerWidth+1,'Long audit title overflows viewport');
 check(rows()[0].textContent.includes('audit-0'),'First page missing first fact');
 await click('下一页');check(rows().length===50 && rows()[0].textContent.includes('audit-50'),'Second page duplicates/skips first row');
 const input=document.querySelector('[aria-label="查找审计事件"]');
 const fill=async value=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const start=performance.now();await fill('audit-500');const searchMs=performance.now()-start;
 check(rows().length===1 && rows()[0].textContent.includes('audit-500'),'Search fails on ID or retains old page');
 await fill('evidence=123');check(rows().length===1 && rows()[0].getAttribute('href').endsWith('evidence=123'),'Search ignores target identity');
 await fill('NO_MATCH_12345');check(!rows().length && document.querySelector('main').textContent.includes('没有匹配的活动'),'Missing empty result feedback');
 await fill('');for(let page=1;page<11;page++)await click('下一页');
 check(rows().length===1 && rows()[0].textContent.includes('audit-500'),'Last page incomplete boundary wrong');
 const next=[...document.querySelectorAll('button')].find(el=>el.textContent.trim()==='下一页');check(next.disabled,'Last-page next remains enabled');
 return {pass:true,width:innerWidth,records:501,maxRendered:50,searchMs,checks:['bounded DOM','long text','paging boundaries','ID and target search','empty result','search resets page']};
})()
