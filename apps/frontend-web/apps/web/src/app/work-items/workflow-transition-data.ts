import type { Workflow } from "../work-management/api"

export const transitionMessages = {
  "en-US": { title: "Workflow transition", target: "Next display state", comment: "Comment / acceptance reason", estimate: "Estimate in minutes (optional)", apply: "Apply transition", refresh: "Refresh current work", hint: "Configured conditions and the original work commands apply. Use the result and evidence controls for submitting evidence.", fields: "Required fields", loading: "Loading…" },
  "zh-CN": { title: "工作流流转", target: "目标显示状态", comment: "批注／验收理由", estimate: "预计分钟数（可选）", apply: "执行流转", refresh: "刷新当前工作项", hint: "流转遵守配置条件及原有工作项命令。提交证据请使用结果与证据操作。", fields: "必填字段", loading: "加载中…" },
  "ms-MY": { title: "Peralihan aliran kerja", target: "Status paparan seterusnya", comment: "Ulasan / sebab penerimaan", estimate: "Anggaran minit (pilihan)", apply: "Gunakan peralihan", refresh: "Muat semula kerja semasa", hint: "Syarat konfigurasi dan arahan kerja asal terpakai. Gunakan kawalan hasil dan bukti untuk menyerahkan bukti.", fields: "Medan wajib", loading: "Memuatkan…" },
}

export function transitionTargets(workflow: Workflow, key: string) {
  const keys = new Set(workflow.transitions.filter(cell => cell.from === key && cell.enabled && cell.manual_allowed).map(cell => cell.to))
  return workflow.states.filter(state => !state.archived && keys.has(state.key) && state.key !== key)
}

export function transitionFields(state: string, estimate: string, result: string | null | undefined, comment: string): Record<string, unknown> {
  if (state === "ACCEPTED") return { result_id: result, reason: comment }
  if (["DRAFT", "READY", "BLOCKED"].includes(state) && estimate !== "") {
    const minutes = Number(estimate)
    if (!Number.isSafeInteger(minutes) || minutes < 15 || minutes > 4800) throw new Error("estimate_minutes")
    return { estimate_minutes: minutes }
  }
  return {}
}
