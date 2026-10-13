(async () => {
 const wait = () => new Promise(resolve => setTimeout(resolve, 180));
 const check = (value, message) => { if (!value) throw Error(message); };
 const go = async path => { location.hash = path; await wait(); };
 await go('/chat?execution_id=W-104');
 if (innerWidth <= 1250) {
  check(!document.querySelector('.preview-resizer'), 'Compact layout exposes desktop splitter');
  document.querySelector('[aria-label="切换会话上下文"]').click(); await wait();
  const dialog = document.querySelector('dialog:modal');
  check(dialog && dialog.getBoundingClientRect().width <= innerWidth, 'Mobile preview is not contained');
  check(dialog.getBoundingClientRect().width >= Math.min(innerWidth - 48, 500), 'Mobile preview remains unnecessarily narrow');
 } else {
  const splitter = () => document.querySelector('[role=separator][aria-label="调整任务预览宽度"]');
  check(splitter(), 'Missing adjustable preview boundary');
  const key = async value => { splitter().focus(); splitter().dispatchEvent(new KeyboardEvent('keydown',{key:value,bubbles:true}));await wait(); };
  await key('Home');
  check(Number(splitter().getAttribute('aria-valuenow')) === 320,'Minimum width not respected');
  await key('ArrowLeft');
  check(Math.abs(document.querySelector('.chat-context').getBoundingClientRect().width - 344) <= 1, 'ArrowLeft did not widen actual preview');
  await key('End');
  check(document.querySelector('.conversation').getBoundingClientRect().width >= 359, 'Preview squashes conversation');
  check(document.documentElement.scrollWidth <= innerWidth + 1,'Max preview creates horizontal overflow');
  await key('Home');await key('ArrowLeft');
  await go('/work');await go('/chat?execution_id=W-201');
  check(Number(splitter().getAttribute('aria-valuenow'))===344,'Width not preserved after remount/task switch');
  check(localStorage.getItem('collaboration-preview-width')==='344','Preference not stored');
 }
 const preview=document.querySelector('.task-previews');
 check(preview,'Preview content missing');
 for (const text of ['成果与版本批注','检查与接受']) check(![...preview.querySelectorAll('button,a')].some(control=>control.textContent.trim()===text),'Unrelated footer action remains');
 check(preview.querySelector('[role=tablist]'),'Preview tabs removed');
 check(!preview.querySelector('[aria-label="添加预览标签"]'),'Manual tab addition remains');
 const content = preview.querySelector('[role=tabpanel]:not([hidden]) .preview-content-scroll');
 check(content.getBoundingClientRect().height > innerHeight * .65, 'Preview reading area is unnecessarily small');
 if (innerWidth > 1250) {
   document.querySelector('[aria-label="展开任务预览"]').click();await wait();
   const expanded=document.querySelector('dialog.preview-dialog');
   check(expanded.getBoundingClientRect().width > innerWidth*.9,'Expanded preview not full width');
   check(expanded.querySelector('.preview-content-scroll').getBoundingClientRect().height > innerHeight*.7,'Expanded content not full height');
   expanded.querySelector('[aria-label="关闭弹窗"]').click();await wait();
   check(!document.querySelector('dialog:modal'),'Expanded preview did not close');
 }

 return {pass:true,width:innerWidth,checks:['responsive preview width','keyboard bounds and retained conversation','width preference across tasks','preview-only footer','preview tabs retained']};
})();
