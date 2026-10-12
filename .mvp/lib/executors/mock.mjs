import path from 'node:path';
import { inside, writeJSON, writeText } from '../files.mjs';

export async function plan() {
  return { invocation: { exitCode: 0, durationMs: 0, outputFile: null }, plan: { workItems: [1, 2].map(n => ({
    id: `W${n}`, title: `模拟工作项 ${n}`, description: '写预设实现与测试', acceptanceRefs: [`AC${n}`], files: ['src/value.mjs', 'tests/value.test.mjs'],
  })) } };
}
export async function execute(ctx) {
  const config = ctx.task.mock ?? {};
  const outputFile = path.join(ctx.runDir, `${ctx.workItem.id}.attempt${ctx.attempt}.call${ctx.call}.last.txt`);
  await writeText(outputFile, 'mock execution\n');
  if (config.executorFailure || (config.executorFailOnce && ctx.call === 1)) {
    return { invocation: { exitCode: 1, durationMs: 0, outputFile, stderr: 'mock executor unavailable' } };
  }
  const bad = config.alwaysFail || (config.failFirst && ctx.workItem.id === 'W1' && ctx.attempt === 1);
  const files = { 'src/value.mjs': `export const value = ${bad ? 0 : 42};\n`,
    'tests/value.test.mjs': `// ${ctx.workItem.id} attempt ${ctx.attempt}\n` + "import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { value } from '../src/value.mjs';\ntest('value is 42', () => assert.equal(value, 42));\n",
    ...(config.files ?? {}), ...(bad ? config.failureFiles ?? {} : config.successFiles ?? {}),
  };
  for (const [file, content] of Object.entries(files)) await writeText(inside(ctx.workspace, file), content);
  await writeJSON(path.join(ctx.workspace, 'claim.json'), {
    workItemId: ctx.workItem.id, summary: '已写入模拟实现', filesChanged: Object.keys(files), selfConfidence: 1,
  });
  await writeJSON(path.join(ctx.runDir, `${ctx.workItem.id}.attempt${ctx.attempt}.context.json`), {
    feedback: ctx.feedback, completed: ctx.completed, prompt: ctx.prompt,
  });
  return { invocation: { exitCode: 0, durationMs: 0, outputFile } };
}
