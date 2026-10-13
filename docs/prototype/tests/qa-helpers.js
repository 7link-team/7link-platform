// Tests exercise the same explicit batch/result actions as the UI.
export function completeValidation(api, workId) {
  const href = api.workCommand(workId, "check");
  const plan = api.read().resources.testplans.find(plan => `/testplans/${plan.id}` === href);
  let batch = plan.batches.find(batch => batch.id === plan.currentBatchId);
  if (batch.items.some(item => item.runId)) {
    api.publishTestReport(plan.id); api.startTestBatch(plan.id);
    const latest = api.read().resources.testplans.find(item => item.id === plan.id);
    batch = latest.batches.find(batch => batch.id === latest.currentBatchId);
  }
  for (const ref of batch.caseRefs) api.recordBatchResult(plan.id, batch.id, ref.caseId, {
    status: "通过", actual: `逐项验证 ${ref.snapshot.expected || ref.snapshot.name || ref.caseId}`,
    evidence: `mock-evidence/${batch.id}/${ref.caseId}`, sourceKind: "平台独立验证", ownerKind: "Agent", ownerId: "verifier", toolVersion: "mock-browser/1", logs: `mock-log/${batch.id}/${ref.caseId}`,
  });
  api.publishTestReport(plan.id);
  return href;
}
