import { useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import type { WorkflowNode } from "./workflow"

// Local text is intentional: incomplete JSON must not overwrite the canonical node.
export function WorkflowNodeProperties({node, onChange}: {node: WorkflowNode; onChange: (node: WorkflowNode) => void}) {
 const t = useTranslations("iam")
 const [logic, setLogic] = useState(JSON.stringify(node.condition ?? true, null, 2))
 const [invalid, setInvalid] = useState(false)
 const field = (key: "input_path" | "escalate_to", label: string) => <label className="block text-sm">{label}<Input value={node[key] ?? ""} onChange={e => onChange({...node, [key]: e.target.value})}/></label>
 return <div className="space-y-3">
  {node.kind === "HUMAN_GATE" && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={node.mandatory ?? false} onChange={e => onChange({...node, mandatory: e.target.checked})}/>{t("templateMandatory")}</label>}
  {["CONDITION", "TRANSFORM", "LOOP"].includes(node.kind) && <div className="space-y-2"><label className="block text-sm">{t("templateLogic")}<Textarea className="font-mono" aria-invalid={invalid} value={logic} onChange={e => {setLogic(e.target.value);setInvalid(false)}}/></label><p className="text-xs text-muted-foreground">{t("templateLogicHint")}</p>{invalid && <p role="alert">{t("templateInvalidJSON")}</p>}<Button variant="outline" onClick={() => {try {const condition: unknown = JSON.parse(logic);onChange({...node, condition});setInvalid(false)}catch {setInvalid(true)}}}>{t("templateApplyProperty")}</Button></div>}
  {node.kind === "FAN_OUT" && <><label className="block text-sm">{t("templateFanItems")}<Textarea value={node.items?.join("\n") ?? ""} onChange={e => onChange({...node, items: e.target.value ? e.target.value.split("\n") : [], input_path: ""})}/></label>{field("input_path", t("templateInputPath"))}<p className="text-xs text-muted-foreground">{t("templateFanHint")}</p></>}
  {node.kind === "LOOP" && <>{(["max_iterations", "budget"] as const).map(key => <label key={key} className="block text-sm">{t(key === "budget" ? "templateLoopBudget" : "templateLoopIterations")}<Input type="number" min={1} max={key === "max_iterations" ? 100 : undefined} value={node[key] ?? ""} onChange={e => onChange({...node, [key]: e.target.valueAsNumber || 0})}/></label>)}{field("escalate_to", t("templateEscalateTo"))}</>}
  {node.kind === "SUBFLOW" && <><label className="block text-sm">{t("templateSubflowID")}<Input value={node.subflow?.workflow_id ?? ""} onChange={e => onChange({...node, subflow: {workflow_id: e.target.value, version: node.subflow?.version ?? 1}})}/></label><label className="block text-sm">{t("templateSubflowVersion")}<Input type="number" min={1} value={node.subflow?.version ?? 1} onChange={e => onChange({...node, subflow: {workflow_id: node.subflow?.workflow_id ?? "", version: e.target.valueAsNumber || 0}})}/></label><p className="text-xs text-muted-foreground">{t("templateSubflowHint")}</p></>}
 </div>
}
