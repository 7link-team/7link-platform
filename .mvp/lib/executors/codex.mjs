import path from 'node:path';
import { findCodex, runCommand } from '../process.mjs';
import { readJSON, writeJSON } from '../files.mjs';
import { planSchema, testSchema, acceptanceSchema } from '../schema.mjs';

export async function plan(ctx) {
  const schemaFile = path.join(ctx.runDir, 'plan.schema.json');
  const outputFile = path.join(ctx.runDir, `plan.call${ctx.call}.last.json`);
  await writeJSON(schemaFile, { ...planSchema, properties: { workItems: { ...planSchema.properties.workItems, maxItems: ctx.task.maxWorkItems } } });
  const command = [...await findCodex(), 'exec', '--output-schema', schemaFile, '-o', outputFile,
    '-C', ctx.workspace, '-s', 'read-only', '--skip-git-repo-check', '-'];
  const invocation = { ...await runCommand(command, { cwd: ctx.workspace, input: ctx.prompt }), outputFile };
  return { invocation, plan: invocation.exitCode === 0 ? await readJSON(outputFile) : null };
}
export async function execute(ctx) {
  const outputFile = path.join(ctx.runDir, `${ctx.workItem.id}.attempt${ctx.attempt}${ctx.call === 1 ? '' : '.retry'}.last.txt`);
  const command = [...await findCodex(), 'exec', '-C', ctx.workspace, '-s', 'workspace-write',
    '--skip-git-repo-check', '-o', outputFile];
  if (ctx.task.executorModel) command.push('-m', ctx.task.executorModel);
  command.push('-');
  return { invocation: { ...await runCommand(command, { cwd: ctx.workspace, input: ctx.prompt }), outputFile } };
}

async function qa(ctx, method, schema, sandbox) {
  const schemaFile = path.join(ctx.runDir, `${method}.schema.json`);
  const outputFile = path.join(ctx.runDir, `${ctx.workItem.id}.round${ctx.attempt}.${method}.call${ctx.call}.json`);
  await writeJSON(schemaFile, schema);
  const command = [...await findCodex(), 'exec', '-C', ctx.workspace, '-s', sandbox,
    '--output-schema', schemaFile, '-o', outputFile, '--skip-git-repo-check'];
  if (ctx.model) command.push('-m', ctx.model);
  for (const file of ctx.screenshots ?? []) command.push('-i', file);
  command.push('-');
  const invocation = { ...await runCommand(command, { cwd: ctx.workspace, input: ctx.prompt }), outputFile };
  return { invocation, conclusion: invocation.exitCode === 0 ? await readJSON(outputFile) : null };
}
export const testAgent = ctx => qa(ctx, 'testAgent', testSchema, 'workspace-write');
export const acceptAgent = ctx => qa(ctx, 'acceptAgent', acceptanceSchema, 'read-only');
