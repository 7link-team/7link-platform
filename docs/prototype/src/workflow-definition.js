// REQ-FLOW-011/012: portable definitions contain no instance or publication state.
export function workflowDefinitionGraph(value) {
  if (!value || !Array.isArray(value.nodes) || !Array.isArray(value.edges) || !value.nodes.length) throw Error("工作流定义须包含节点与连线数组。");
  const nodes = value.nodes.map(node => {
    if (!node || typeof node.id !== "string" || !node.data || typeof node.data !== "object" || Array.isArray(node.data)) throw Error("节点定义格式无效。");
    const data = structuredClone(node.data);
    for (const key of ["status", "executionId", "runId", "logs", "results", "attempts"]) delete data[key];
    return { id: node.id, type: "task", position: node.position || {x:0,y:0}, data };
  });
  const edges = value.edges.map(edge => {
    if (!edge || typeof edge.source !== "string" || typeof edge.target !== "string") throw Error("连线定义格式无效。");
    return Object.fromEntries(["id","source","target","sourceHandle","targetHandle","data","label","type"].filter(key=>edge[key] !== undefined).map(key=>[key,structuredClone(edge[key])]));
  });
  return {nodes,edges};
}
export function serializeWorkflowDefinition(graph, source) {
  return JSON.stringify({format:"workflow-definition",schemaVersion:1,source,...workflowDefinitionGraph(graph)},null,2);
}
export function parseWorkflowDefinition(text) {
  let value;
  try { value=JSON.parse(text); } catch { throw Error("定义文件不是有效 JSON。"); }
  if (value?.format !== "workflow-definition" || value.schemaVersion !== 1) throw Error("请选择受支持的工作流定义文件；列表摘要不能导入。");
  return workflowDefinitionGraph(value);
}
export function assertWorkflowBaseline(flow, graph) {
  if (!Number.isSafeInteger(graph.baseVersion) || graph.baseVersion !== (flow.flowVersion || 0)) throw Error(`工作流版本冲突：编辑基线 v${graph.baseVersion ?? "未知"}，当前 v${flow.flowVersion || 0}。请保留修改并重新打开最新定义。`);
}
export function replaceWorkflowDraft(flow, graph, expectedDraft) {
  assertWorkflowBaseline(flow,graph);
  if (JSON.stringify(flow.flowDraft) !== expectedDraft) throw Error("草案已被其他编辑更新，请重新打开并比较修改。");
  flow.flowDraft=structuredClone(graph);
}
export function workflowPatch(before, after) {
  const changes=[];
  for(const collection of ["nodes","edges"]) {
    const key=item=>item.id || `${item.source}:${item.sourceHandle || ""}:${item.target}:${item.targetHandle || ""}`;
    const old=new Map(before[collection].map(item=>[key(item),item])), next=new Map(after[collection].map(item=>[key(item),item]));
    for(const id of new Set([...old.keys(),...next.keys()])) if(JSON.stringify(old.get(id))!==JSON.stringify(next.get(id))) changes.push({collection,id,before:structuredClone(old.get(id)),after:structuredClone(next.get(id))});
  }
  return changes;
}
export function validateLoopBoundary(policy) {
  if(!policy?.enabled) return [];
  const issues=[];
  for(const key of ["maxAttempts","maxSeconds","maxCost"]) if(!Number.isFinite(Number(policy[key])) || Number(policy[key])<=0 || (key!=="maxCost"&&!Number.isSafeInteger(Number(policy[key])))) issues.push(`${key} 必须为正${key==="maxCost"?"数":"整数"}`);
  if(!String(policy.exitCondition || "").trim())issues.push("请定义循环退出条件");
  return issues;
}
export function evaluateLoopBoundary(policy, attempts) {
  const issues=validateLoopBoundary(policy);if(!policy?.enabled || issues.length)throw Error(issues[0] || "请先启用并配置循环边界。");
  if(!Array.isArray(attempts) || attempts.some(a=>![a.seconds,a.cost].every(n=>typeof n==="number"&&Number.isFinite(n)&&n>=0)||!String(a.solution || "").trim()))throw Error("每次尝试须记录方案和非负时间、成本。");
  const seconds=attempts.reduce((n,a)=>n+a.seconds,0),cost=attempts.reduce((n,a)=>n+a.cost,0);
  const exited=attempts.at(-1)?.exitSatisfied===true;
  const exceeded=attempts.length>=Number(policy.maxAttempts)||seconds>=Number(policy.maxSeconds)||cost>=Number(policy.maxCost);
  return {attempts:attempts.length,seconds,cost,status:exited?"已退出":exceeded?"暂停并升级 Human":"边界内",requiresHuman:!exited&&exceeded};
}
