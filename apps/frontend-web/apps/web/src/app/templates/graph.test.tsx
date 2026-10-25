import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { WorkflowGraph } from "./graph"
import { softwareDefinition } from "./workflow"
test("graph projects actual node states and has keyboard node controls", () => {const workflow = softwareDefinition().workflow;const html = renderToStaticMarkup(<WorkflowGraph workflow={workflow} runs={[{node_id: "build", expansion_key: "build", state: "IN_PROGRESS"}]} onSelect={() => {}}/>);expect(html).toContain('role="button"');expect(html).toContain('tabindex="0"');expect(html).toContain("IN_PROGRESS");expect(html).toContain("Client acceptance");expect(html).not.toContain("ACCEPTED");const readonly = renderToStaticMarkup(<WorkflowGraph workflow={workflow}/>);expect(readonly).not.toContain('role="button"')})

test("root graph never substitutes a same-named child run", () => {
 const workflow=softwareDefinition().workflow
 const html=renderToStaticMarkup(<WorkflowGraph workflow={workflow} runs={[{node_id:"build",expansion_key:"5:build",state:"WAITING"},{node_id:"build",expansion_key:"5:child/4:body/5:build",state:"ACCEPTED"}]}/>)
 expect(html).toContain("WAITING")
 expect(html).not.toContain("ACCEPTED")
 workflow.nodes[0]!.id="构建"
 const unicode=renderToStaticMarkup(<WorkflowGraph workflow={workflow} runs={[{node_id:"构建",expansion_key:"6:构建",state:"IN_PROGRESS"}]}/>)
 expect(unicode).toContain("IN_PROGRESS")
})
