(async () => {
 const wait = () => new Promise(resolve => setTimeout(resolve, 180));
 const check = (value, message) => { if (!value) throw Error(message); };
 const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const go = async path => { location.hash = path; await wait(); };
 const click = async (label, scope = document) => {
  const button = [...scope.querySelectorAll('button')].find(button => button.textContent.trim() === label);
  check(button, `Missing ${label}`); button.click(); await wait();
 };
 const fill = async (value) => {
  const input = document.querySelector('.composer textarea');
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input,value);
  input.dispatchEvent(new Event('input',{bubbles:true})); await wait();
 };
 const checks = [];
 const executions=state().executions.filter(item=>item.workId&&item.id!==item.workId);
 check(executions.length,'Need actual execution identities for the access-boundary checks');
 for(const execution of executions){
  await go(`/chat?execution_id=${encodeURIComponent(execution.id)}`);
  check(document.querySelector('main').textContent.includes(execution.id),'Exact execution view missing');
  check(!document.querySelector('.message-actions'),'Execution exposes ordinary message-sharing controls');
 }
 checks.push('execution records use their own identity and do not offer ordinary message sharing');
 for (const projectId of ['launch','film','novel']) {
  await go(`/chat?channel_id=${projectId}`);
  const marker = `share-${projectId}-${Date.now()}`;
  await fill(marker); await click('发送',document.querySelector('.composer'));
  const message = [...document.querySelectorAll('.message')].find(message => message.textContent.includes(marker));
  await click('共享',message);
  const project = state().projects.find(project => project.id === projectId);
  check(project,`Missing project ${projectId}`);
  check(document.querySelector('dialog').textContent.includes(`接收频道：${project.name}`),'Wrong default target preview');
  await click('确认共享选定消息',document.querySelector('dialog'));
  const shared = state().messages.at(-1);
  check(shared.channel === projectId,`Shared channel message into wrong project ${shared.channel}`);
  check(shared.author === '林予' && !shared.file && !shared.work && !shared.thread, 'Shared unintended fields');
  await go(`/chat?channel_id=${projectId}`);
  check(document.querySelector('.messages').textContent.includes(marker),'Shared text not readable in target');
  check(!document.querySelector('main').textContent.includes('会话不存在'),'Target channel missing');
  checks.push(`${projectId} explicit preview, delivery and target view`);
 }
 await go('/chat?channel_id=team');
 const shell=document.querySelector('.app-shell');
 document.querySelector('.scope-select [aria-label="项目"]').click();await wait();
 const projectOption=[...document.querySelectorAll('[role=option]')].find(option=>option.textContent.trim()===state().projects.find(project=>project.id==='film').name);
 check(projectOption,'Film option missing from the single project picker');projectOption.click();await wait();
 check(location.hash.split('?')[0]==='#/chat'&&new URLSearchParams(location.hash.split('?')[1]).get('scope')==='film','Project picker did not retain chat with an exact scope URL');
 let directory=document.querySelector('.channel-list');
 if(!directory||!directory.getClientRects().length){document.querySelector('[aria-label="打开会话列表"]').click();await wait();directory=document.querySelector('dialog .channel-list');}
 const channelLink=[...directory.querySelectorAll('button,a')].find(el=>el.querySelector('b')?.textContent.trim()===state().projects.find(project=>project.id==='film').name);
 check(channelLink,'Film channel missing from conversation directory');channelLink.click();await wait();
 check(document.querySelector('.app-shell')===shell&&new URLSearchParams(location.hash.split('?')[1]).get('channel_id')==='film'&&state().project==='film','Channel selection lost the shell, URL identity or selected scope');
 check(document.querySelector('.chat-title')?.textContent.includes('品牌短片') || document.querySelector('main').textContent.includes('向海而生'),'Project channel did not switch in place');
 checks.push('single project picker and channel directory switch in place with precise URLs');
 await go('/chat?channel_id=private');
 const message = document.querySelector('.message');
 await click('共享',message);
 check(document.querySelector('dialog [aria-label="接收项目"]').textContent.includes('请选择'),'Private message silently assigned project');
 const count = state().messages.length;
 await click('确认共享选定消息',document.querySelector('dialog'));
 check(state().messages.length === count && document.querySelector('dialog'),'Missing target was not blocked');
 document.querySelector('dialog [aria-label="接收项目"]').click();await wait();
 const option = [...document.querySelectorAll('[role=option]')].find(option => option.textContent.includes('品牌短片'));
 check(option,'Film project option missing');option.click();await wait();
 await click('确认共享选定消息',document.querySelector('dialog'));
 check(state().messages.at(-1).channel==='film','Explicit private text target not respected');
 checks.push('private sharing requires explicit project; unselected history excluded');
 await go('/settings');
 document.querySelector('[aria-label="角色视角"]').click();await wait();
 [...document.querySelectorAll('[role=option]')].find(option => option.textContent.trim()==='external').click();await wait();
 for (const execution of executions) {
  await go(`/chat?channel_id=external&execution_id=${encodeURIComponent(execution.id)}`);
  check(!document.querySelector('.composer') && !document.querySelector('.execution-event') && !document.querySelector('.execution-record'),'External query combination exposes execution');
  check(document.querySelector('main').textContent.includes('不在共享范围'),'Missing access boundary notice');
 }
 await go('/chat?channel_id=external');
 check(!!document.querySelector('.composer'),'Authorized external conversation blocked');
 await go('/settings');
 document.querySelector('[aria-label="角色视角"]').click();await wait();
 [...document.querySelectorAll('[role=option]')].find(option => option.textContent.trim()==='owner').click();await wait();
 checks.push('external channel parameter cannot expose execution logs');
 check(document.documentElement.scrollWidth<=innerWidth,'Sharing page overflows');
 return {pass:true,checks,width:innerWidth};
})();
