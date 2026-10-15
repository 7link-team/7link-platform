// Shared list presentation contract. Exercises real controls and persisted view state.
(async () => {
  const wait=()=>new Promise(resolve=>setTimeout(resolve,160));
  const checks=[];const check=(ok,name)=>{checks.push({name,pass:Boolean(ok)});if(!ok)throw Error(name);};
  location.hash='/p/launch/work-items';await wait();
  const list=()=>document.querySelector('.resource-data-list[aria-label="任务列表"]');
  check(list(),'Task list is available');
  const headers=()=>[...list().querySelectorAll('thead th')];
  const originalCount=headers().length;
  const chooser=()=>list().querySelector('.data-list-columns');
  chooser().querySelector('summary').click();await wait();
  const toggle=chooser().querySelectorAll('input[type="checkbox"]')[1];
  const label=toggle.closest('label').textContent;
  toggle.click();await wait();
  check(headers().length===originalCount-1&&!headers().some(h=>h.textContent.trim().startsWith(label)),'Hiding a column removes header and matching cells');
  check([...list().querySelectorAll('tbody tr')].every(row=>row.cells.length===originalCount-1),'All rows stay aligned after hiding a column');
  const saved=location.hash;location.hash='/overview';await wait();location.hash=saved;await wait();
  check(headers().length===originalCount-1,'Column preference survives leaving and restoring the view');
  chooser().querySelector('summary').click();await wait();
  [...chooser().querySelectorAll('button')].find(b=>b.textContent.trim()==='恢复默认列').click();await wait();
  check(headers().length===originalCount,'Restoring columns retains mandatory ordinal and actions');
  chooser().open=false;
  const sortName=list().querySelector('.data-list-sort').textContent.replace(/[↑↓↕]/g,'').trim();
  const sortButton=()=>[...list().querySelectorAll('.data-list-sort')].find(b=>b.textContent.replace(/[↑↓↕]/g,'').trim()===sortName);
  for(let i=0;i<3&&sortButton().closest('th').getAttribute('aria-sort')!=='none';i++){sortButton().click();await wait();}
  check(sortButton().closest('th').getAttribute('aria-sort')==='none','Sorting can return to default order');
  for(const direction of ['ascending','descending','none']){sortButton().click();await wait();check(sortButton().closest('th').getAttribute('aria-sort')===direction,`Sort cycle reaches ${direction}`);}
  check(list().querySelector('.list-refresh-split > .btn').getBoundingClientRect().height<=48,'Refresh label stays on one line');
  const region=list().querySelector('[role="region"]'),footer=list().querySelector('.data-list-pagination');
  const before=footer.getBoundingClientRect().top;region.scrollTop=10000;region.scrollLeft=10000;region.dispatchEvent(new Event('scroll',{bubbles:true}));await wait();
  check(Math.abs(footer.getBoundingClientRect().top-before)<2,'Scrolling data does not move pagination');
  check(getComputedStyle(region.querySelector('th')).position==='sticky'&&getComputedStyle(region.querySelector('tbody td')).position==='sticky','Header and ordinal remain frozen');
  check(document.documentElement.scrollWidth<=innerWidth+1,'Wide table stays inside viewport');
  if(innerWidth>=768)check(footer.getBoundingClientRect().bottom<=innerHeight+1,'Desktop pagination stays inside visible viewport');
  check(list().querySelector('.data-list-filters')&&!list().querySelector('.data-list-filters .data-list-primary-actions'),'Filtering and primary actions have separate rows');
  const frame=list().querySelector('.data-list-viewport');
  const initialStyle=frame.getAttribute('style');
  frame.style.width='280px';frame.style.height='160px';frame.style.flex='none';
  region.scrollTop=0;region.scrollLeft=0;region.dispatchEvent(new Event('scroll',{bubbles:true}));await wait();
  const cue=edge=>getComputedStyle(frame.querySelector(`.data-list-shadow-${edge}`)).opacity==='1';
  check([...frame.querySelectorAll('.data-list-shadow')].every(el=>getComputedStyle(el).backgroundImage.includes('linear-gradient')),'Every cue resolves to a painted theme-aware gradient');
  check(region.scrollWidth>region.clientWidth&&region.scrollHeight>region.clientHeight,'Narrow viewport genuinely overflows on both axes');
  check(!cue('left')&&cue('right')&&!cue('top')&&cue('bottom'),'Only right and bottom cues appear at origin');
  region.scrollLeft=(region.scrollWidth-region.clientWidth)/2;region.scrollTop=(region.scrollHeight-region.clientHeight)/2;region.dispatchEvent(new Event('scroll',{bubbles:true}));await wait();
  check(['left','right','top','bottom'].every(cue),'Both sides on both axes show cues between boundaries');
  region.scrollLeft=region.scrollWidth;region.scrollTop=region.scrollHeight;region.dispatchEvent(new Event('scroll',{bubbles:true}));await wait();
  check(cue('left')&&!cue('right')&&cue('top')&&!cue('bottom'),'End-edge cues disappear at actual scroll boundaries');
  check([...frame.querySelectorAll('.data-list-shadow')].every(el=>getComputedStyle(el).pointerEvents==='none'),'Cues do not intercept row or scroll interaction');
  frame.style.width='2000px';frame.style.height='2000px';frame.style.maxHeight='none';await wait();
  check(!['left','right','top','bottom'].some(cue),'Resize removing overflow removes every cue');
  if(initialStyle===null)frame.removeAttribute('style');else frame.setAttribute('style',initialStyle);
  await wait();
  return {pass:true,checks};
})()
