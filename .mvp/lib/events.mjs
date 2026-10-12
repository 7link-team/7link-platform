import * as fs from 'node:fs/promises';
import path from 'node:path';
import { sha256 } from './files.mjs';

export async function checkChain(dir) {
  let content;
  try { content = await fs.readFile(path.join(dir, 'events.jsonl'), 'utf8'); }
  catch (err) { if (err.code === 'ENOENT') return { ok: true, count: 0, lastHash: '' }; throw err; }
  let prevHash = '', count = 0;
  const lines = content.split('\n');
  if (lines.at(-1) !== '') return { ok: false, count, error: '事件日志末行不完整' };
  lines.pop();
  for (const line of lines) {
    try {
      const event = JSON.parse(line), { hash, ...rest } = event;
      if (event.seq !== count + 1 || event.prevHash !== prevHash || hash !== sha256(prevHash + JSON.stringify(rest))) {
        return { ok: false, count, error: `第 ${count + 1} 行哈希或序号不匹配` };
      }
      prevHash = hash;
      count++;
    } catch { return { ok: false, count, error: `第 ${count + 1} 行不是合法 JSON` }; }
  }
  return { ok: true, count, lastHash: prevHash };
}
export async function emit(dir, type, data = {}, workItemId, actor = { kind: 'SYSTEM', id: 'myr' }) {
  const chain = await checkChain(dir);
  if (!chain.ok) throw new Error(`事件链损坏，拒绝追加: ${chain.error}`);
  const event = { seq: chain.count + 1, ts: new Date().toISOString(), type, actor,
    ...(workItemId ? { workItemId } : {}), data, prevHash: chain.lastHash };
  event.hash = sha256(event.prevHash + JSON.stringify(event));
  await fs.appendFile(path.join(dir, 'events.jsonl'), JSON.stringify(event) + '\n', 'utf8');
  return event;
}
