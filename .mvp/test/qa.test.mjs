import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { screenshots, validConclusion } from '../lib/qa.mjs';
import { writeText } from '../lib/files.mjs';

test('没有浏览器时记录未能截图，不启动外部进程', async () => {
  const keys = ['MYR_BROWSER', 'PATH', 'PROGRAMFILES(X86)', 'PROGRAMFILES', 'LOCALAPPDATA'];
  const saved = keys.map(key => [key, process.env[key]]);
  try {
    for (const key of keys) delete process.env[key];
    const result = await screenshots(os.tmpdir(), os.tmpdir(), { id: 'W1' }, 1, { page: 'index.html', viewports: [[900, 900]] });
    assert.deepEqual(result.files, []);
    assert.deepEqual(result.invocations, []);
    assert.deepEqual(result.unverified, ['未能截图：未找到浏览器']);
  } finally {
    for (const [key, value] of saved) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  }
});

const installed = process.platform === 'win32' ? 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe' : null;
const hasBrowser = installed && await fs.access(installed).then(() => true, () => false);
test('真实 Edge 通过 localhost 截取实际 HTML，两种视口的 PNG 与证据命名正确', { skip: !hasBrowser || process.env.MYR_REAL_BROWSER_TEST !== '1', timeout: 90_000 }, async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'myr-screenshot-'));
  t.after(() => fs.rm(root, { recursive: true, force: true, maxRetries: 5 }));
  const workspace = path.join(root, 'workspace');
  await writeText(path.join(workspace, 'index.html'), '<!doctype html><meta charset="utf-8"><style>body{background:#123456;color:white}</style><h1>Actual delivered page</h1>');
  const original = process.env.MYR_BROWSER;
  process.env.MYR_BROWSER = installed;
  let result;
  try { result = await screenshots(root, workspace, { id: 'W1' }, 2, { page: 'index.html', viewports: [[900, 900], [420, 800]] }); }
  finally { if (original === undefined) delete process.env.MYR_BROWSER; else process.env.MYR_BROWSER = original; }
  assert.deepEqual(result.unverified, [], JSON.stringify(result.invocations));
  assert.deepEqual(result.notes, ['420x800：页面在 420×800 的 iframe 中按真实视口宽度渲染']);
  assert.deepEqual(result.files.map(file => path.basename(file)), ['W1.round2.900x900.png', 'W1.round2.420x800.png']);
  for (let n = 0; n < result.files.length; n++) {
    const bytes = await fs.readFile(result.files[n]);
    assert.deepEqual([bytes.readUInt32BE(16), bytes.readUInt32BE(20)], [[900, 900], [420, 800]][n]);
    const invocation = result.invocations[n];
    assert.equal(invocation.exitCode, 0);
    assert.match(invocation.command.at(-1), n === 0 ? /^http:\/\/127\.0\.0\.1:\d+\/index.html$/ : /^http:\/\/127\.0\.0\.1:\d+\/__myr_viewport_frame\?w=420&h=800$/);
    await assert.rejects(fetch(invocation.command.at(-1)), /fetch failed/);
  }
});

test('截图流程提供实际页面、拒绝私有/越界路径、保留 PNG 证据并关闭静态服务', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'myr-screenshot-flow-'));
  t.after(() => fs.rm(root, { recursive: true, force: true, maxRetries: 5 }));
  const workspace = path.join(root, 'workspace'), html = '<h1>Delivered page</h1>';
  await writeText(path.join(workspace, 'index.html'), html);
  await writeText(path.join(workspace, '.private'), 'secret');
  const original = process.env.MYR_BROWSER;
  process.env.MYR_BROWSER = 'mock-browser';
  const urls = [];
  let result;
  try {
    result = await screenshots(root, workspace, { id: 'W2' }, 3, { page: 'index.html', viewports: [[900, 900], [420, 800]] }, async command => {
      const url = command.at(-1);
      urls.push(url);
      if (url.includes('/__myr_viewport_frame?')) {
        assert.match(await (await fetch(url)).text(), /<iframe src="\/index\.html" width="420" height="800"><\/iframe>/);
        assert.equal(await (await fetch(new URL('/index.html', url))).text(), html);
      } else assert.equal(await (await fetch(url)).text(), html);
      assert.equal((await fetch(new URL('/.private', url))).status, 404);
      assert.equal((await fetch(new URL('/%2e%2e%2foutside', url))).status, 404);
      assert.ok(command.includes('--headless=new'));
      assert.ok(command.includes('--virtual-time-budget=4000'));
      const [width, height] = command.find(arg => arg.startsWith('--window-size=')).slice(14).split(',').map(Number);
      const file = command.find(arg => arg.startsWith('--screenshot=')).slice(13);
      const bytes = Buffer.alloc(24);
      Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]).copy(bytes);
      bytes.writeUInt32BE(width, 16); bytes.writeUInt32BE(height, 20);
      await fs.writeFile(file, bytes);
      return { command, exitCode: 0 };
    });
  } finally { if (original === undefined) delete process.env.MYR_BROWSER; else process.env.MYR_BROWSER = original; }
  assert.deepEqual(result.unverified, []);
  assert.deepEqual(result.files.map(file => path.basename(file)), ['W2.round3.900x900.png', 'W2.round3.420x800.png']);
  for (const url of urls) await assert.rejects(fetch(url), /fetch failed/);
});

test('QA JSON 契约拒绝非法严重级别、证据类型和验收 verdict', () => {
  assert.equal(validConclusion({ pass: true, findings: [] }), true);
  assert.equal(validConclusion({ pass: false, criteria: [], uxFindings: [] }, true), true);
  for (const value of [null, {}, { pass: 'true', findings: [] }, { pass: true, findings: [{}] }, { pass: true, findings: [null] },
    { pass: true, findings: [{ severity: 'CRITICAL', title: '', evidence: '', expected: '' }] },
    { pass: true, findings: [{ severity: 'MINOR', title: '', evidence: 3, expected: '' }] }]) assert.equal(validConclusion(value), false);
  assert.equal(validConclusion({ pass: true, criteria: [{ ref: 'AC1', verdict: 'OK', evidence: '' }], uxFindings: [] }, true), false);
  assert.equal(validConclusion({ pass: true, criteria: [null], uxFindings: [] }, true), false);
});
