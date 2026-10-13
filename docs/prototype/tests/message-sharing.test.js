import { test, expect, beforeEach } from 'bun:test';
import { messageProject, prepareMessageShare } from '../src/message-sharing';
const entries = new Map();
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
 getItem: key => entries.get(key) || null, setItem: (key, value) => entries.set(key, value), removeItem: key => entries.delete(key),
}});
const { read, reset, update, shareMessage } = await import('../src/store');
beforeEach(() => reset());
test('each execution shares to its own explicitly selected project without copying attachments or impersonating the author', () => {
 for (const work of read().work) {
  if (work.project === 'private') continue;
  const messageId = `sharing-${work.id}`;
  update(s => s.messages.push({ id: messageId, channel: `exec-${work.id}`, author: 'Atlas', kind: 'agent', text: 'Selected text', file: 'secret', work: work.id, thread: 'hidden', history: ['private'] }));
  const source = read().messages.at(-1);
  expect(messageProject(read(), source)?.id).toBe(work.project);
  const result = shareMessage({ tenantId: read().activeTenant, messageId, expectedText: source.text, target: 'project', projectId: work.project });
  expect(result.channel).toBe(work.project);
  expect(result.author).toBe('林予');
  expect(result.text).toBe('共享自 Atlas：Selected text');
  for (const field of ['file', 'work', 'thread', 'history']) expect(field in result).toBe(false);
 }
});
test('unknown or missing targets, changed/recalled text, private external sharing reject atomically', () => {
 const source = read().messages.find(message => message.channel === 'private');
 const base = { tenantId: read().activeTenant, messageId: source.id, expectedText: source.text, target: 'project', projectId: 'launch' };
 for (const patch of [{tenantId:'other-tenant'}, {projectId:''},{projectId:'other-tenant-project'},{target:'external'},{expectedText:'old text'},{messageId:'missing'}]) {
  const before = JSON.stringify(read());
  expect(() => shareMessage({...base,...patch})).toThrow();
  expect(JSON.stringify(read())).toBe(before);
 }
 update(s => s.messages.find(message => message.id === source.id).recalled = true);
 expect(() => shareMessage(base)).toThrow('撤回');
});
test('tenant/global/private messages do not silently select a project', () => {
 for (const channel of ['private','team','external','missing','exec-missing']) {
  expect(messageProject(read(), {channel})).toBeNull();
 }
 const source = read().messages.find(message => message.channel === 'team');
 expect(() => prepareMessageShare(read(), {tenantId:read().activeTenant,messageId:source.id,expectedText:source.text,target:'project'})).toThrow('明确');
});
