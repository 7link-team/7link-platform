(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 160));
  const check = (value, message) => { if (!value) throw Error(message); };
  location.hash = '/chat?channel_id=launch';
  await wait();
  if (innerWidth < 768) { document.querySelector('[aria-label="打开会话列表"]').click(); await wait(); }
  const sidebar = document.querySelector('.channel-list');
  const button = label => sidebar.querySelector(`button[aria-label="${label}"]`);
  const url = location.href, historyLength = history.length;
  check(!sidebar.querySelector('input[aria-label="查找频道"]') && !sidebar.querySelector('[role="combobox"]'), 'Directory controls must be collapsed by default');
  const listTop = sidebar.querySelector('.panel-button').getBoundingClientRect().top;
  button('搜索频道').click(); await wait();
  let input = sidebar.querySelector('input[aria-label="查找频道"]');
  check(document.activeElement === input, 'Opening search must focus its input');
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, 'no_matching_channel_987');
  input.dispatchEvent(new Event('input', { bubbles: true })); await wait();
  check(sidebar.textContent.includes('没有匹配的频道'), 'No-match search must explain empty results');
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await wait();
  check(document.activeElement === button('搜索频道') && !sidebar.querySelector('input'), 'Escape must collapse search and restore focus');
  check(button('搜索频道').classList.contains('is-filtered'), 'Hidden search must retain a visible applied-filter marker');
  const reset = [...sidebar.querySelectorAll('button')].find(item => item.textContent === '清除筛选');
  reset.click(); await wait();
  check(sidebar.querySelector('.panel-button').getBoundingClientRect().top === listTop, 'Reset must restore compact list position');
  button('筛选频道').click(); await wait();
  sidebar.querySelector('[aria-label="频道状态"]').click(); await wait();
  const all = [...document.querySelectorAll('[role="option"]')].find(item => item.textContent.trim() === '全部频道');
  check(all, 'Custom channel status options missing'); all.click(); await wait();
  button('筛选频道').click(); await wait();
  check(button('筛选频道').classList.contains('is-filtered') && !sidebar.querySelector('[role="combobox"]'), 'Collapsed status filter must retain its marker');
  check(location.hash.split('?')[0] === '#/chat' && new URLSearchParams(location.hash.split('?')[1]).get('channel_id') === 'launch' && history.length === historyLength, 'Directory tools must preserve conversation and history');
  check(document.documentElement.scrollWidth <= innerWidth, 'Directory tools overflow viewport');
  if (innerWidth < 768) {
    const target = sidebar.querySelector('.panel-button');
    const name = target.querySelector('b').textContent;
    target.click(); await wait();
    check(!document.querySelector('dialog[open]') && document.querySelector('.chat-heading h1').textContent.includes(name), 'Mobile selection must close directory and open its conversation');
    check(document.activeElement === document.querySelector('[aria-label="打开会话列表"]'), 'Mobile directory must restore focus');
    check(location.hash.split('?')[0] === '#/chat' && history.length === historyLength+1 && JSON.parse(localStorage.getItem('collaboration-prototype-v1')).channels.some(channel=>channel.id===new URLSearchParams(location.hash.split('?')[1]).get('channel_id') && channel.name===name), 'Mobile selection must synchronize selected channel URL in place');
  }
  return { pass: true, width: innerWidth, checks: ['compact default', 'search focus / empty state / Escape', 'hidden applied filter marker', 'reset list position', 'custom status picker', 'target URL synchronization without scroll history entries', 'no viewport overflow'] };
})();
