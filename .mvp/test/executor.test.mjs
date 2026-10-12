import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { testAgent, acceptAgent, execute } from '../lib/executors/codex.mjs';
import { readJSON, writeText } from '../lib/files.mjs';

test('Codex QA 参数契约：独立 sandbox、JSON schema、验收模型与多张 -i 证据', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'myr-codex-adapter-'));
  t.after(() => fs.rm(root, { recursive: true, force: true, maxRetries: 5 }));
  const entry = path.join(root, process.platform === 'win32' ? 'codex.js' : 'codex');
  await writeText(entry, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2), output = args[args.indexOf('-o') + 1];
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', () => {
  fs.writeFileSync(output + '.args.json', JSON.stringify({ args, input }));
  fs.writeFileSync(output, JSON.stringify(output.includes('acceptAgent') ? { pass: true, criteria: [], uxFindings: [] } : { pass: true, findings: [] }));
  process.stdout.write('model: stub-model\\n');
});
`);
  if (process.platform === 'win32') await writeText(path.join(root, 'codex.cmd'), '@node "%dp0%\\codex.js"\n');
  else await fs.chmod(entry, 0o755);
  const original = process.env.PATH;
  process.env.PATH = root + path.delimiter + (original ?? '');
  try {
    const ctx = { runDir: root, workspace: root, task: { executorModel: 'implement-model' }, workItem: { id: 'W1' }, attempt: 2, call: 1, prompt: 'requirement and current item' };
    const testing = await testAgent(ctx);
    assert.equal(testing.invocation.exitCode, 0);
    assert.equal(testing.invocation.reportedModel, 'stub-model');
    const testArgs = await readJSON(testing.invocation.outputFile + '.args.json');
    assert.equal(testArgs.args[testArgs.args.indexOf('-s') + 1], 'workspace-write');
    assert.equal(testArgs.args[testArgs.args.indexOf('-C') + 1], root);
    assert.equal(testArgs.input, ctx.prompt);
    assert.deepEqual((await readJSON(testArgs.args[testArgs.args.indexOf('--output-schema') + 1])).required, ['pass', 'findings']);
    const images = [path.join(root, '900.png'), path.join(root, '420.png')];
    const acceptance = await acceptAgent({ ...ctx, model: 'product-model', screenshots: images });
    const acceptArgs = (await readJSON(acceptance.invocation.outputFile + '.args.json')).args;
    assert.equal(acceptArgs[acceptArgs.indexOf('-s') + 1], 'read-only');
    assert.equal(acceptArgs[acceptArgs.indexOf('-m') + 1], 'product-model');
    assert.deepEqual(acceptArgs.flatMap((arg, index) => arg === '-i' ? [acceptArgs[index + 1]] : []), images);
    assert.deepEqual(acceptance.conclusion, { pass: true, criteria: [], uxFindings: [] });
    const implementation = await execute(ctx);
    const implementArgs = (await readJSON(implementation.invocation.outputFile + '.args.json')).args;
    assert.equal(implementArgs[implementArgs.indexOf('-m') + 1], 'implement-model');
  } finally { if (original === undefined) delete process.env.PATH; else process.env.PATH = original; }
});
