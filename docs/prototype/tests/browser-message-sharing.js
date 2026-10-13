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
 for (const [workId, projectId] of [['W-104','launch'],['W-201','film'],['W-301','novel']]) {
  await go(`/chat?execution_id=${workId}`);
  const marker = `share-${workId}-${Date.now()}`;
  await fill(marker); await click('发送',document.querySelector('.composer'));
  const message = [...document.querySelectorAll('.message')].find(message => message.textContent.includes(marker));
  await click('共享',message);
  const project = state().projects.find(project => project.id === projectId);
  check(project,`Missing project ${projectId}`);
  check(document.querySelector('dialog').textContent.includes(`接收频道：${project.name}`),'Wrong default target preview');
  await click('确认共享选定消息',document.querySelector('dialog'));
  const shared = state().messages.at(-1);
  check(shared.channel === projectId,`Shared ${workId} into wrong project ${shared.channel}`);
  check(shared.author === '林予' && !shared.file && !shared.work && !shared.thread, 'Shared unintended fields');
  await go(`/chat?channel_id=${projectId}`);
  check(document.querySelector('.messages').textContent.includes(marker),'Shared text not readable in target');
  check(!document.querySelector('main').textContent.includes('会话不存在'),'Target channel missing');
  checks.push(`${projectId} explicit preview, delivery and target view`);
 }
 await go('/chat?channel_id=team');
 const url=location.href, historyLength=history.length;
 if (matchMedia('(max-width: 1023px)').matches) {document.querySelector('[aria-label="展开导航"]').click();await wait();}
 await click(state().projects.find(project=>project.id==='film').name,document.querySelector('.sidebar nav'));
 check(location.href===url && history.length===historyLength,'Project channel sidebar changed URL/history');
 check(document.querySelector('.chat-title')?.textContent.includes('品牌短片') || document.querySelector('main').textContent.includes('向海而生'),'Project channel did not switch in place');
 checks.push('project channel sidebar switches in place');
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
 for (const workId of ['W-104','W-201','W-301']) {
  await go(`/chat?channel_id=external&execution_id=${workId}`);
  check(!document.querySelector('.composer') && !document.querySelector('.execution-events'),'External query combination exposes execution');
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
