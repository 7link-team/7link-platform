import path from 'node:path';
import { writeText } from './files.mjs';
import { checkChain } from './events.mjs';

export async function report(dir, state) {
  const chain = await checkChain(dir);
  const lines = [`# myr 交付报告：${state.task.name}`, '', `- Run：${state.runId}`, `- 状态：${state.status}`,
    `- 耗时：${((Date.now() - Date.parse(state.startedAt)) / 1000).toFixed(1)} 秒（含人工等待）`,
    `- 事件链：${chain.ok ? `通过，${chain.count} 条` : `失败：${chain.error}`}`, '',
    'L1 是 Agent 声明，L2 是程序验证，L3 是裁决，L4 是甲方人工验收。自动策略不代表人。', ''];
  for (const item of state.workItems) {
    lines.push(`## ${item.id} ${item.title}`, '', `- 状态：${item.status}；尝试次数：${item.attempts.length}`,
      `- L1 声明：${item.attempts.some(a => a.claim) ? '有（不作为完成证明）' : '无'}`,
      `- L2 验证：${item.status === 'ACCEPTED' || item.status === 'VERIFIED' ? '通过' : '未完成'}`,
      `- L3 接受：${item.status === 'ACCEPTED' ? '已接受' : '未接受'}；授权来源：${item.authoritySource ?? '无'}`,
      `- L4 甲方：${state.status === 'COMPLETED' ? '已确认' : '未完成'}`, '');
    for (const attempt of item.attempts) {
      lines.push(`### 尝试 ${attempt.number}`, '', `- Commit：${attempt.commit ?? '无'}`,
        `- 声明：${attempt.claim?.summary ?? attempt.claimError ?? '无'}`,
        `- 执行：${attempt.invocations?.map(x => `exit=${x.exitCode}，${x.durationMs}ms，消息=${x.outputFile ?? '无'}`).join('；') ?? '未完成'}`,
        `- 输入反馈：${JSON.stringify(attempt.feedback ?? null)}`);
      for (const result of attempt.verification ?? []) {
        lines.push(`- 验证 ${result.name}：exit=${result.exitCode}，超时=${result.timedOut ?? false}，输出 SHA256=${result.outputSha256 ?? '无'}`);
        if (result.exitCode !== 0) lines.push('', '```text', (result.stderr || '') + (result.stdout || '') + (result.error || ''), '```');
      }
      lines.push('');
    }
    lines.push(`- 当前反馈：${JSON.stringify(item.feedback ?? null)}`, '');
  }
  lines.push('## 甲方自动验收', '');
  if (state.acceptance) {
    lines.push(`- 结果：${state.acceptance.ok ? '通过，等待人工确认' : '失败'}`,
      `- 验收证据 Commit：${state.acceptance.commit}`, `- 文件指纹：${JSON.stringify(state.acceptance.hashes)}`,
      `- 文件完整性：${state.acceptance.integrity ? '通过' : '失败'}`, '', '```text',
      state.acceptance.result.stdout ?? '', state.acceptance.result.stderr ?? '', state.acceptance.result.error ?? '', '```');
  } else lines.push('尚未运行。');
  if (state.client) lines.push('', `甲方裁决：${JSON.stringify(state.client)}`);
  if (state.independentVerification) lines.push('', `独立复验：${JSON.stringify(state.independentVerification, null, 2)}`);
  lines.push('', '提示：哈希链检测日志行修改；没有外部签名/锚点，不能证明整条日志未被重写或截尾。', '');
  await writeText(path.join(dir, 'report.md'), lines.join('\n'));
}
