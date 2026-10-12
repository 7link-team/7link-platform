#!/usr/bin/env node
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { start, resume, review, accept, load, independentVerify } from './lib/engine.mjs';

const usage = '用法：node myr.mjs run <taskDir> [--executor codex|mock] [--auto-approve-if-verified] [--run-id id] [--acceptor-model model] [--qa-screenshot index.html --qa-viewports 900x900,420x800]\n       node myr.mjs review <runDir> <W1> approve|reject [--category X --detail 内容 --by 姓名]\n       node myr.mjs resume|verify|status <runDir>\n       node myr.mjs accept <runDir> --by 姓名 [--reject --detail 内容]';
function parse(args) {
  const positional = [], options = {};
  const names = { '--executor': 'executor', '--run-id': 'runId', '--by': 'by', '--category': 'category', '--detail': 'detail',
    '--acceptor-model': 'acceptorModel', '--qa-screenshot': 'qaScreenshot', '--qa-viewports': 'qaViewports' };
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--auto-approve-if-verified') options.autoApprove = true;
    else if (args[i] === '--reject') options.reject = true;
    else if (names[args[i]]) {
      const key = names[args[i]], value = args[++i];
      if (!value || value.startsWith('--')) throw new Error('选项缺少值');
      options[key] = value;
    } else if (args[i].startsWith('--')) throw new Error(`未知选项 ${args[i]}`);
    else positional.push(args[i]);
  }
  return { positional, options };
}
async function locked(dir, operation) {
  const lock = path.join(dir, '.myr.lock');
  try { await fs.mkdir(lock); }
  catch (err) { if (err.code === 'EEXIST') throw new Error('run 正在被操作；若进程已崩溃，确认没有运行进程后删除 .myr.lock'); throw err; }
  try { return await operation(); }
  finally { await fs.rm(lock, { recursive: true, force: true }); }
}
export async function main(args = process.argv.slice(2)) {
  const { positional: [command, target, id, decision, ...extra], options } = parse(args);
  if (!command || !target || extra.length || (command !== 'review' && (id || decision))) throw new Error(usage);
  if (command === 'run') {
    const result = await start(target, options);
    console.log(`Run：${result.dir}`);
    printNext(result.code, result.dir);
    return result.code;
  }
  const dir = path.resolve(target);
  if (command === 'status') {
    const state = await load(dir);
    console.log(`${state.runId}: ${state.status}\n` + state.workItems.map(x => `${x.id}: ${x.status}, attempts=${x.attempts.length}, authority=${x.authoritySource ?? '无'}`).join('\n')
      + `\n甲方自动验收：${state.acceptance ? (state.acceptance.ok ? '通过' : '失败') : '未运行'}；L4：${state.client?.decision ?? '未完成'}`);
    return 0;
  }
  return locked(dir, async () => {
    if (command === 'resume') { const code = await resume(dir); printNext(code, dir); return code; }
    if (command === 'review') { await review(dir, id, decision, options); console.log(`已记录人工裁决；继续：node myr.mjs resume "${dir}"`); return 0; }
    if (command === 'accept') { const state = await accept(dir, options); console.log(state.status); return 0; }
    if (command === 'verify') { const result = await independentVerify(dir); console.log(JSON.stringify(result, null, 2)); return result.ok ? 0 : 1; }
    throw new Error(usage);
  });
}
function printNext(code, dir) {
  if (code === 2) console.log(`NEEDS_HUMAN_REVIEW：node myr.mjs review "${dir}" <W编号> approve --by <姓名>，然后 resume`);
  else if (code === 3) console.log(`NEEDS_HUMAN：使用 review reject --category X --detail "反馈" 后 resume；规划故障可直接 resume`);
  else console.log(`查看状态：node myr.mjs status "${dir}"；报告：${path.join(dir, 'report.md')}`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then(code => { process.exitCode = code; }, err => { console.error(err.message); process.exitCode = 1; });
}
