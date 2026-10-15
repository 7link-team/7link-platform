// Seeded untrusted drafts test rendering, never task or report acceptance.
(async()=>{
 const wait=()=>new Promise(resolve=>setTimeout(resolve,180));
 const check=(ok,message)=>{if(!ok)throw Error(message);};
 const go=async path=>{location.hash=path;await wait();};
 const click=async(text,root=document)=>{const button=[...root.querySelectorAll('button,a')].find(el=>el.textContent.trim()===text);check(button&&!button.disabled,`Missing ${text}`);button.click();await wait();};
 await go('/reports/RENDER-LONG');
 check(document.querySelector('main').textContent.includes('B1'),'Unavailable fixture evidence masquerades as accepted');
 const body=document.querySelector('[data-report-content]');check(body,'Structured reader absent');
 check(body.querySelector('h3').textContent.length===192,'Long title lost');
 check(document.documentElement.scrollWidth<=innerWidth+2,'Long title overflows viewport');
 check(body.querySelectorAll('article').length===52,'Large content list silently truncated');
 check(getComputedStyle(body.querySelector('article')).contentVisibility==='auto','Large list lacks offscreen rendering containment');
 const adjacent=body.querySelectorAll('article');check(parseFloat(getComputedStyle(adjacent[2]).marginTop)>=16,'Unfinished entries lack visual separation');
 const raw=localStorage.getItem('collaboration-prototype-v1');
 await click('编辑');check(document.querySelector('[data-report-content-editor]'),'Editor absent');
 check(document.querySelector('[name="完成标题 1"]').value.length===192,'Editor lost long title');
 check(document.querySelector('dialog').scrollWidth<=document.querySelector('dialog').clientWidth+2,'Editor horizontal overflow');
 await click('取消',document.querySelector('dialog'));check(localStorage.getItem('collaboration-prototype-v1')===raw,'Read/cancel rewrote fixture');
 await go('/reports/RENDER-MALFORMED');check(document.querySelector('main').textContent.includes('报告章节无法读取'),'Malformed content lacks explicit error');check(!document.body.textContent.includes('页面暂时无法显示'),'Malformed report crashed whole page');
 const before=localStorage.getItem('collaboration-prototype-v1');await click('编辑');check(document.querySelector('dialog').textContent.includes('原始内容仍保留'),'Recovered editor dropped corrupt content');await click('保存',document.querySelector('dialog'));check(document.querySelector('dialog [role=alert]'),'Invalid save has no error');check(localStorage.getItem('collaboration-prototype-v1')===before,'Failed save replaced malformed raw body');
 const error=document.querySelector('dialog .error'),parse=value=>{const parts=value.match(/[\d.]+/g)?.map(Number);check(parts?.length>=3,`Unsupported color ${value}`);return [...parts.slice(0,3),parts[3]??1];};
 let background=[255,255,255];const ancestors=[];for(let current=error;current;current=current.parentElement)ancestors.unshift(current);
 for(const node of ancestors){const color=parse(getComputedStyle(node).backgroundColor);background=background.map((v,i)=>color[i]*color[3]+v*(1-color[3]));}
 const lum=rgb=>rgb.slice(0,3).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;}).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);
 const fg=lum(parse(getComputedStyle(error).color)),bg=lum(background),contrast=(Math.max(fg,bg)+.05)/(Math.min(fg,bg)+.05);check(contrast>=4.5,`Error contrast ${contrast} below4.5`);
 await click('取消',document.querySelector('dialog'));
 for(let i=0;i<16;i++){
  await go('/reports/RENDER-PLAN-'+i);
  check(document.querySelector('main').textContent.includes('固定下期计划快照损坏'),'Corrupt adopted Plan lacks explicit error '+i);
  check(!document.body.textContent.includes('页面暂时无法显示'),'Corrupt Plan crashes report detail '+i);
  const saved=localStorage.getItem('collaboration-prototype-v1');
  await click('编辑');check(document.querySelector('dialog').textContent.includes('固定下期计划快照损坏'),'Corrupt Plan editor lacks error '+i);
  await click('保存',document.querySelector('dialog'));check(document.querySelector('dialog [role=alert]'),'Corrupt Plan save lacks error '+i);
  check(localStorage.getItem('collaboration-prototype-v1')===saved,'Rejected corrupt Plan save changes storage '+i);
  await click('取消',document.querySelector('dialog'));
 }
 return {pass:true,width:innerWidth,errorContrast:contrast,fixture:'untrusted drafts, no acceptance claims',checks:['192-character title fits','51 entries remain accessible','offscreen containment','entry spacing','editor fits','cancel retains body','malformed reader and editor errors','invalid save atomic','small error text contrast']};
})()
