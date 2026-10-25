import { describe, expect, test } from "bun:test"
import { connectWorkflow, removeWorkflowNode, parseWorkflowJSON, parseWorkflowProposal, softwareDefinition, workflowPatch, workflowIssues, newWorkflowNode, workflowNodeKinds, type TemplateVersion } from "./workflow"
describe("workflow single source", () => {
 test("canvas connections preserve DAG and removal clears incident edges", () => {
  const workflow=softwareDefinition().workflow
  for (const [source,target] of [["client","build"],["build","team"],["build","build"],["missing","client"]]) expect(connectWorkflow(workflow,source!,target!)).toBe(workflow)
  const next=connectWorkflow(workflow,"build","client")
  expect(next.edges).toHaveLength(3)
  expect(workflow.edges).toHaveLength(2)
  expect(removeWorkflowNode(next,"build")).toBe(next)
  const removed=removeWorkflowNode(next,"team")
  expect(removed.nodes.map(n=>n.id)).toEqual(["build","client"])
  expect(removed.edges.map(e=>[e.source,e.target])).toEqual([["build","client"]])
  expect(parseWorkflowJSON(JSON.stringify(removed))).toEqual(removed)
 })
 test("JSON export envelope reloads identical semantics and positions", () => {const definition = softwareDefinition();const version: TemplateVersion = {id: "9007199254740993", key: "software", state: "DRAFT", hash: "a".repeat(64), version: 1, definition};expect(parseWorkflowJSON(JSON.stringify(version))).toEqual(definition.workflow);expect(parseWorkflowJSON(JSON.stringify(definition.workflow))).toEqual(definition.workflow);for (const value of ["null", "{}", '{"nodes":[],"edges":[]}', "{"]) expect(() => parseWorkflowJSON(value)).toThrow()})
 test("patch preserves base hash and creates explicit semantic operations", () => {const definition = softwareDefinition();const version: TemplateVersion = {id: "1", key: "software", state: "DRAFT", hash: "a".repeat(64), version: 1, definition};const next = structuredClone(definition.workflow);next.nodes = next.nodes.filter(n => n.id !== "team");next.edges = next.edges.filter(e => e.source !== "team" && e.target !== "team");next.nodes.push({id: "new", kind: "HUMAN_GATE", name: "Review", gate: "TEAM"});next.edges.push({id: "new-edge", source: "build", target: "new"});const patch = workflowPatch(version, next, "Approved edit");expect(patch.base_hash).toBe(version.hash);expect(patch.base_version).toBe(1);expect(patch.reason).toBe("Approved edit");expect(patch.operations).toContainEqual({kind: "remove_node", id: "team"});expect(patch.operations).toContainEqual({kind: "remove_edge", id: "build-team"});expect(patch.operations).toContainEqual({kind: "add_node", id: "new", node: next.nodes.at(-1)});expect(patch.operations.at(-1)).toEqual({kind: "update_workflow", workflow: next});expect(version.definition.workflow.nodes).toHaveLength(3)})
})

test("rich graph round trip preserves bounded runtime properties and explicit condition branches", () => {
 const definition=softwareDefinition(), w=definition.workflow
 let tail="build"
 for(const kind of workflowNodeKinds.filter(k=>!["WORK_ITEM","TEAM","CLIENT"].includes(k))) {
  const n=newWorkflowNode(kind,kind,w.nodes.length)
  if(kind==="FAN_OUT") n.items=["chapter-1","chapter-2"]
  if(kind==="LOOP") {n.max_iterations=3;n.budget=7;n.escalate_to="owner";n.condition={"<":[{"var":"attempt"},3]}}
  if(kind==="SUBFLOW") n.subflow={workflow_id:"approved-child",version:4}
  w.nodes.push(n);w.edges.push({id:`from-${tail}`,source:tail,target:n.id,...(kind==="CONDITION" ? {branch:"true"} : {})});tail=n.id
 }
 expect(workflowIssues(w)).toEqual([])
 const imported=parseWorkflowJSON(JSON.stringify(w))
 const base:TemplateVersion={id:"11",key:"software",state:"DRAFT",hash:"frozen-hash",version:1,definition:softwareDefinition()}
 const patch=workflowPatch(base,imported,"Reviewed proposed graph")
 expect(patch.operations.at(-1)?.workflow).toEqual(w)
 expect(patch.base_hash).toBe("frozen-hash")
 expect(imported.nodes.find(n=>n.kind==="LOOP")).toMatchObject({max_iterations:3,budget:7,escalate_to:"owner"})
 expect(imported.nodes.find(n=>n.kind==="SUBFLOW")?.subflow).toEqual({workflow_id:"approved-child",version:4})
})

test("diagnostics identify unsafe graphs and do not mutate the proposed or current workflow", () => {
 const w=softwareDefinition().workflow
 const loop=newWorkflowNode("LOOP","Retry",3);loop.max_iterations=101;loop.budget=0
 const fan=newWorkflowNode("FAN_OUT","Expand",4);fan.items=["one","one"];fan.input_path="items"
 const condition=newWorkflowNode("CONDITION","Unsafe",5);condition.condition={exec:"shell"}
 w.nodes.push(loop,fan,condition,newWorkflowNode("SUBFLOW","Child",6))
 w.nodes[0]!.criteria=[]
 w.edges.push({id:"cycle",source:"client",target:"build"})
 const before=JSON.stringify(w), codes=workflowIssues(w).map(i=>i.code)
 for(const code of ["templateIssueLoop","templateIssueFan","templateIssueLogic","templateIssueSubflow","templateIssueWork","templateIssueGraph"] as const) expect(codes).toContain(code)
 expect(JSON.stringify(w)).toBe(before)
 expect(workflowIssues(softwareDefinition().workflow)).toEqual([])
})

test("untrusted proposals reject malformed fields before rendering or applying", () => {
 for(const mutate of [
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.nodes=[null as never]},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.nodes[0]!.objective=7 as never},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.nodes[0]!.criteria=[null as never]},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.nodes[0]!.items={} as never},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.nodes[0]!.subflow=null as never},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.edges=[null as never]},
  (w:ReturnType<typeof softwareDefinition>["workflow"])=>{w.name=42 as never},
 ]) {const w=softwareDefinition().workflow;mutate(w);expect(()=>parseWorkflowJSON(JSON.stringify(w))).toThrow()}
})

test("assistant patch proposals are detached and fenced by the saved hash and current local draft",()=>{
 const base:TemplateVersion={id:"11",key:"software",state:"DRAFT",hash:"saved-hash",version:9,definition:softwareDefinition()}
 const current=base.definition.workflow, before=JSON.stringify(base)
 const patch={base_hash:base.hash,base_version:current.version,reason:"Suggested change",operations:[{kind:"update_node",id:"build",node:{...current.nodes[0]!,name:"Proposed work"}}]}
 const proposed=parseWorkflowProposal(JSON.stringify(patch),base,current)
 expect(proposed.nodes[0]!.name).toBe("Proposed work")
 expect(JSON.stringify(base)).toBe(before)
 expect(()=>parseWorkflowProposal(JSON.stringify({...patch,base_hash:"stale"}),base,current)).toThrow()
 expect(()=>parseWorkflowProposal(JSON.stringify({...patch,base_version:99}),base,current)).toThrow()
 expect(()=>parseWorkflowProposal(JSON.stringify(patch),base,{...current,name:"Unsaved change"})).toThrow()
 for(const operation of [{kind:"publish"},{kind:"update_node",id:"missing",node:current.nodes[0]},{kind:"add_node",id:"build",node:current.nodes[0]},{kind:"remove_edge",id:"missing"},null]) expect(()=>parseWorkflowProposal(JSON.stringify({...patch,operations:[operation]}),base,current)).toThrow()
 expect(JSON.stringify(base)).toBe(before)
})
