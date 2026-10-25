export type WorkflowNode = {id: string; kind: string; name: string; mandatory?: boolean; objective?: string; criteria?: {text: string; verification_method: string}[]; gate?: string; condition?: unknown; items?: string[]; input_path?: string; max_iterations?: number; budget?: number; escalate_to?: string; subflow?: {workflow_id: string; version: number}; x?: number; y?: number}
export type WorkflowEdge = {id: string; source: string; target: string; branch?: string}
export type WorkflowDef = {schema_version: number; workflow_id: string; version: number; name: string; parameters: Record<string, unknown>; nodes: WorkflowNode[]; edges: WorkflowEdge[]; entry_node_id: string; output_bindings: Record<string, string>}
export type Definition = {domain: string; semver: string; workflow: WorkflowDef; metadata: Record<string, unknown>; constitution_seed: string}
export type TemplateVersion = {id: string; key: string; name?:string; origin?:"FIRST_PARTY"|"TEAM"; based_on_version_id?:string; based_on_sha256?:string; state: string; hash: string; version: number; definition: Definition}
export type WorkflowInstance = {id: string; project_id: string; template_version_id: string; hash: string; constitution_id: string; workflow: WorkflowDef; runs: {node_id: string; expansion_key: string; work_item_id?: string; state: string; reason?: string}[]}
export type WorkflowPatch = {base_version: number; base_hash: string; reason: string; operations: ({kind: string; id?: string; node?: WorkflowNode; edge?: WorkflowEdge; workflow?: WorkflowDef})[]}

// An assistant may propose a patch; it cannot bypass review, save or publication.
export function parseWorkflowProposal(text: string, base: TemplateVersion, current: WorkflowDef): WorkflowDef {
 const value: unknown = JSON.parse(text)
 if (!value || typeof value !== "object" || !("operations" in value)) return parseWorkflowJSON(text)
 const patch = value as WorkflowPatch
 if (patch.base_hash !== base.hash || patch.base_version !== base.definition.workflow.version || JSON.stringify(current) !== JSON.stringify(base.definition.workflow) || !Array.isArray(patch.operations) || !patch.operations.length) throw new Error("Invalid workflow patch base")
 let next = structuredClone(current)
 for (const op of patch.operations) {
  if (!op || typeof op !== "object") throw new Error("Invalid workflow patch operation")
  const node = next.nodes.findIndex(n => n.id === op.id), edge = next.edges.findIndex(e => e.id === op.id)
  switch (op.kind) {
   case "update_workflow": if (!op.workflow) throw new Error("Invalid workflow patch");next = structuredClone(op.workflow);break
   case "add_node": if (!op.node || node >= 0 || op.id !== op.node.id) throw new Error("Invalid workflow patch");next.nodes.push(structuredClone(op.node));break
   case "update_node": if (!op.node || node < 0 || op.id !== op.node.id) throw new Error("Invalid workflow patch");next.nodes[node] = structuredClone(op.node);break
   case "remove_node": if (node < 0) throw new Error("Invalid workflow patch");next.nodes.splice(node, 1);break
   case "add_edge": if (!op.edge || edge >= 0 || op.id !== op.edge.id) throw new Error("Invalid workflow patch");next.edges.push(structuredClone(op.edge));break
   case "update_edge": if (!op.edge || edge < 0 || op.id !== op.edge.id) throw new Error("Invalid workflow patch");next.edges[edge] = structuredClone(op.edge);break
   case "remove_edge": if (edge < 0) throw new Error("Invalid workflow patch");next.edges.splice(edge, 1);break
   default: throw new Error("Unsupported workflow patch operation")
  }
 }
 return parseWorkflowJSON(JSON.stringify(next))
}

export function connectWorkflow(workflow: WorkflowDef, source: string, target: string): WorkflowDef {
 if (!source || source === target || !workflow.nodes.some(n => n.id === source) || !workflow.nodes.some(n => n.id === target) || workflow.edges.some(e => e.source === source && e.target === target)) return workflow
 // A dependency edge cannot introduce a cycle; bounded iteration is a LOOP node.
 const pending = [target], visited = new Set<string>()
 while (pending.length) {const id = pending.pop()!;if (id === source) return workflow;if (visited.has(id)) continue;visited.add(id);pending.push(...workflow.edges.filter(e => e.source === id).map(e => e.target))}
 return {...workflow, edges: [...workflow.edges, {id: crypto.randomUUID(), source, target}]}
}

export function removeWorkflowNode(workflow: WorkflowDef, id: string): WorkflowDef {
 if (id === workflow.entry_node_id) return workflow
 return {...workflow, nodes: workflow.nodes.filter(n => n.id !== id), edges: workflow.edges.filter(e => e.source !== id && e.target !== id)}
}

export function parseWorkflowJSON(text: string): WorkflowDef {
 const value: unknown = JSON.parse(text)
 if (!value || typeof value !== "object") throw new Error("Invalid workflow JSON")
 const envelope = value as Partial<TemplateVersion>
 const workflow = envelope.definition?.workflow ?? value as WorkflowDef
 if (!Array.isArray(workflow.nodes) || !Array.isArray(workflow.edges) || typeof workflow.workflow_id !== "string" || typeof workflow.entry_node_id !== "string") throw new Error("Invalid workflow JSON")
 if (workflow.nodes.some(n => !n || typeof n !== "object" || typeof n.id !== "string" || typeof n.name !== "string" || typeof n.kind !== "string" || (n.criteria !== undefined && (!Array.isArray(n.criteria) || n.criteria.some(c => !c || typeof c.text !== "string" || typeof c.verification_method !== "string")))) || workflow.edges.some(e => !e || typeof e !== "object" || typeof e.id !== "string" || typeof e.source !== "string" || typeof e.target !== "string")) throw new Error("Invalid workflow JSON")
 if (typeof workflow.name !== "string" || workflow.nodes.some(n =>
  [n.objective, n.gate, n.input_path, n.escalate_to].some(v => v !== undefined && typeof v !== "string") ||
  [n.max_iterations, n.budget, n.x, n.y].some(v => v !== undefined && (typeof v !== "number" || !Number.isFinite(v))) ||
  (n.mandatory !== undefined && typeof n.mandatory !== "boolean") ||
  (n.items !== undefined && (!Array.isArray(n.items) || n.items.some(v => typeof v !== "string"))) ||
  (n.subflow !== undefined && (!n.subflow || typeof n.subflow !== "object" || typeof n.subflow.workflow_id !== "string" || typeof n.subflow.version !== "number"))) ||
  workflow.edges.some(e => e.branch !== undefined && typeof e.branch !== "string")) throw new Error("Invalid workflow JSON")
 return workflow
}

export const workflowNodeKinds = ["WORK_ITEM", "TEAM", "CLIENT", "CONDITION", "FAN_OUT", "TRANSFORM", "LOOP", "SUBFLOW"] as const
export type WorkflowNodeKind = typeof workflowNodeKinds[number]
export function newWorkflowNode(kind: WorkflowNodeKind, name: string, index: number): WorkflowNode {
 return {id: crypto.randomUUID(), name, kind: kind === "TEAM" || kind === "CLIENT" ? "HUMAN_GATE" : kind, x: 40, y: 40 + index * 100,
  ...(kind === "WORK_ITEM" ? {objective: "", criteria: [{text: "", verification_method: ""}]} : {}),
  ...(kind === "TEAM" || kind === "CLIENT" ? {gate: kind} : {}),
  ...(["CONDITION", "TRANSFORM", "LOOP"].includes(kind) ? {condition: true} : {}),
  ...(kind === "FAN_OUT" ? {items: []} : {}),
  ...(kind === "LOOP" ? {max_iterations: 1, budget: 1, escalate_to: ""} : {}),
  ...(kind === "SUBFLOW" ? {subflow: {workflow_id: "", version: 1}} : {})}
}

export type WorkflowIssue = {path: string; code: "templateIssueNode" | "templateIssueWork" | "templateIssueLogic" | "templateIssueFan" | "templateIssueLoop" | "templateIssueSubflow" | "templateIssueEdge" | "templateIssueGraph" | "templateIssueGates"}
// Advisory editor diagnostics. Authorized publication additionally resolves child
// versions, inherited gates and immutable bindings on the server.
export function workflowIssues(w: WorkflowDef): WorkflowIssue[] {
 const issues: WorkflowIssue[] = []
 const add = (path: string, code: WorkflowIssue["code"]) => issues.push({path, code})
 const logicOK = (v: unknown, depth = 0): boolean => {
  if (depth > 32) return false
  if (Array.isArray(v)) return v.every(x => logicOK(x, depth + 1))
  if (v !== null && typeof v === "object") {const entries = Object.entries(v);return entries.length === 1 && ["var", "==", "!=", ">", ">=", "<", "<=", "and", "or", "!", "in"].includes(entries[0]![0]) && logicOK(entries[0]![1], depth + 1)}
  return v !== undefined
 }
 const ids = new Set<string>(), gates = new Set<string>()
 for (const [i, n] of w.nodes.entries()) {
  const path = `nodes[${i}] (${n.id})`
  if (!n.id || !n.name.trim() || ids.has(n.id) || !["WORK_ITEM", "HUMAN_GATE", "CONDITION", "FAN_OUT", "TRANSFORM", "LOOP", "SUBFLOW"].includes(n.kind) || (n.mandatory && n.kind !== "HUMAN_GATE")) add(path, "templateIssueNode")
  ids.add(n.id)
  if (n.kind === "HUMAN_GATE") {if (!["TEAM", "CLIENT"].includes(n.gate ?? "")) add(path, "templateIssueGates");gates.add(n.gate ?? "")}
  if (n.kind === "WORK_ITEM" && (!n.objective?.trim() || !n.criteria?.length || n.criteria.length > 50 || n.criteria.some(c => !c.text.trim() || !c.verification_method.trim()))) add(path, "templateIssueWork")
  if (["CONDITION", "TRANSFORM", "LOOP"].includes(n.kind) && !logicOK(n.condition)) add(path, "templateIssueLogic")
  if (n.kind === "FAN_OUT" && ((!!n.items?.length === !!n.input_path) || (n.items?.length ?? 0) > 1000 || n.items?.some(x => !x) || new Set(n.items).size !== (n.items?.length ?? 0))) add(path, "templateIssueFan")
  if (n.kind === "LOOP" && (!Number.isInteger(n.max_iterations) || n.max_iterations! < 1 || n.max_iterations! > 100 || !Number.isInteger(n.budget) || n.budget! < 1 || !n.escalate_to)) add(path, "templateIssueLoop")
  if (n.kind === "SUBFLOW" && (!n.subflow?.workflow_id || !Number.isInteger(n.subflow.version) || n.subflow.version < 1)) add(path, "templateIssueSubflow")
 }
 if (!gates.has("TEAM") || !gates.has("CLIENT")) add("nodes", "templateIssueGates")
 const edges = new Set<string>(), adjacency = new Map<string, string[]>(), degree = new Map([...ids].map(id => [id, 0]))
 for (const [i, e] of w.edges.entries()) {
  if (!e.id || edges.has(e.id) || !ids.has(e.source) || !ids.has(e.target) || e.source === e.target) add(`edges[${i}]`, "templateIssueEdge")
  edges.add(e.id);adjacency.set(e.source, [...(adjacency.get(e.source) ?? []), e.target]);degree.set(e.target, (degree.get(e.target) ?? 0) + 1)
 }
 const reached = new Set<string>(), pending = [w.entry_node_id]
 while (pending.length) {const id = pending.pop()!;if (reached.has(id)) continue;reached.add(id);pending.push(...(adjacency.get(id) ?? []))}
 const queue = [...ids].filter(id => degree.get(id) === 0)
 let count = 0
 while (queue.length) {const id = queue.pop()!;count++;for (const to of adjacency.get(id) ?? []) {degree.set(to, degree.get(to)! - 1);if (degree.get(to) === 0) queue.push(to)}}
 if (w.schema_version !== 1 || !Number.isInteger(w.version) || w.version < 1 || !w.workflow_id.trim() || !w.name?.trim() || !ids.has(w.entry_node_id) || w.edges.some(e => e.target === w.entry_node_id) || !w.nodes.length || w.nodes.length > 1000 || w.edges.length > 2000 || reached.size !== ids.size || count !== ids.size) add("workflow", "templateIssueGraph")
 return issues
}

// The canvas and JSON editor both edit this object; neither stores its own graph.
export function workflowPatch(base: TemplateVersion, next: WorkflowDef, reason: string): WorkflowPatch {
  const operations: WorkflowPatch["operations"] = []
  for (const edge of base.definition.workflow.edges) if (!next.edges.some(e => e.id === edge.id)) operations.push({kind: "remove_edge", id: edge.id})
  for (const node of base.definition.workflow.nodes) if (!next.nodes.some(n => n.id === node.id)) operations.push({kind: "remove_node", id: node.id})
  for (const node of next.nodes) operations.push({kind: base.definition.workflow.nodes.some(n => n.id === node.id) ? "update_node" : "add_node", id: node.id, node})
  for (const edge of next.edges) operations.push({kind: base.definition.workflow.edges.some(e => e.id === edge.id) ? "update_edge" : "add_edge", id: edge.id, edge})
  operations.push({kind: "update_workflow", workflow: next})
  return {base_version: base.definition.workflow.version, base_hash: base.hash, reason, operations}
}

export function softwareDefinition(): Definition {
 return {domain: "SOFTWARE", semver: "1.0.0", constitution_seed: "# Working agreement\nHuman approval is required for team and client acceptance. Evidence must reference actual results.", metadata: {method_variants: ["agile", "waterfall"]}, workflow: {schema_version: 1, workflow_id: crypto.randomUUID(), version: 1, name: "Software delivery", parameters: {method: "agile"}, entry_node_id: "build", output_bindings: {}, nodes: [{id: "build", kind: "WORK_ITEM", name: "Implement and verify", objective: "Implement the approved brief and provide reproducible verification evidence", criteria: [{text: "Approved requirements are implemented", verification_method: "Run relevant tests and attach actual results"}], x: 40, y: 60}, {id: "team", kind: "HUMAN_GATE", name: "Team review", gate: "TEAM", x: 310, y: 60}, {id: "client", kind: "HUMAN_GATE", name: "Client acceptance", gate: "CLIENT", x: 580, y: 60}], edges: [{id: "build-team", source: "build", target: "team"}, {id: "team-client", source: "team", target: "client"}]}}
}
