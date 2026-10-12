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
  lines.push('## 内部质量门（D113）', '',
    `- 执行者模型：${state.executorModel ?? 'CLI_DEFAULT_UNRESOLVED'}；验收者模型：${state.acceptorModel ?? 'CLI_DEFAULT_UNRESOLVED'}`);
  if (!state.executorModel || state.executorModel === state.acceptorModel || [state.executorModel, state.acceptorModel].includes('CLI_DEFAULT_UNRESOLVED')) {
    lines.push('- 未满足跨模型家族要求，已降级为不同主体（CLI 默认模型无法确认时保守降级）。');
  }
  const rounds = state.workItems.flatMap(item => item.attempts.map(attempt => ({ item, attempt })));
  const intercepted = rounds.filter(({ item, attempt }) => {
    if (attempt.internalQA?.ok !== false) return false;
    const recovered = item.attempts.find(next => next.number > attempt.number && next.internalQA?.ok);
    return recovered && !item.attempts.some(next => next.number >= attempt.number && next.number < recovered.number && next.escalatedToHuman);
  });
  const started = state.workItems.filter(item => item.attempts.length).length;
  const firstPass = state.workItems.filter(item => item.attempts[0]?.internalQA?.ok).length;
  lines.push(`- 内部一次通过率：${started ? (firstPass / started * 100).toFixed(1) : '0.0'}%`,
    `- 平均内部轮次：${started ? (rounds.length / started).toFixed(2) : '0.00'}`,
    `- 内部拦截的问题数（未打扰人）：${intercepted.reduce((total, { attempt }) => {
      const qa = attempt.internalQA;
      const stage = qa[qa.failedStage];
      return total + Math.max(1, stage?.conclusion?.findings?.length ?? stage?.conclusion?.uxFindings?.length ?? 0,
        stage?.conclusion?.criteria?.filter(c => c.verdict === 'FAIL').length ?? 0, stage?.violations?.length ?? 0,
        (stage?.results ?? stage?.verification ?? []).filter(r => r.exitCode !== 0 || r.error || r.timedOut).length,
        stage?.integrityViolations?.length ?? 0);
    }, 0)}`, '');
  for (const { item, attempt } of rounds) {
    lines.push(`### ${item.id} 第 ${attempt.number} 轮`, '');
    lines.push(`- 执行者模型：${attempt.executorModel ?? '未确认'}；验收者模型：${attempt.internalQA?.AGENT_ACCEPTANCE?.model ?? '未执行'}`);
    for (const stage of ['AUTOMATED', 'AGENT_TEST', 'AGENT_ACCEPTANCE']) {
      const value = attempt.internalQA?.[stage];
      lines.push(`- ${stage}：${value ? (value.ok ? '通过' : '失败') : '未执行'}；发现/证据：${JSON.stringify(value ?? null)}`);
    }
    lines.push(`- 截图：${attempt.internalQA?.screenshots?.files.map(file => path.basename(file)).join('、') || '无'}`,
      `- 未验证：${JSON.stringify(attempt.internalQA?.screenshots?.unverified ?? [])}`, '');
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
