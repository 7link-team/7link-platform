import { iamApi } from "../../lib/iam-api"

export type VerifierLayer = "AUTOMATED" | "AI_ASSISTED" | "HUMAN"
export type VerifierPolicy = { work_item_types: string[]; risk_levels: string[]; project_ids: string[] | null }
export type VerifierSpec = { argv?: string[]; workdir?: string; env?: Record<string, string>; report_globs?: string[]; report_format?: string; stdout_tail_bytes?: number; tool_version_argv?: string[]; agent_id?: string; prompt_template?: string; max_cost_micros?: number; instructions?: string; eligible_role_keys?: string[] }
export type VerifierVersion = { id: string; version_no: number; spec: VerifierSpec; timeout_ms: number; change_note: string; created_by: string; created_at: string }
export type Verifier = { id: string; key: string; name: string; layer: VerifierLayer; state: "ACTIVE" | "ARCHIVED"; required: boolean; applies_to: VerifierPolicy; version: number; current_version_no: number; current: VerifierVersion }
export type VerifierDefinition = Pick<Verifier, "key" | "name" | "layer" | "state" | "required" | "applies_to"> & Pick<VerifierVersion, "spec" | "timeout_ms" | "change_note">
export type RegistryPage<T> = { items: T[]; has_more: boolean; next_cursor?: string }
const id = (value: string) => { if (!/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error("Invalid identifier"); return value }
export function verifierRegistryAPI(tenantID: string, client: Pick<typeof iamApi, "deliveryResultRequest"> = iamApi) {
  const root = `/tenants/${id(tenantID)}/verifiers`
  return {
    list: (query: Record<string, string>, signal?: AbortSignal) => client.deliveryResultRequest<RegistryPage<Verifier>>({ method: "GET", url: `${root}?${new URLSearchParams({ limit: "50", ...query })}` }, { signal }),
    get: (verifierID: string, signal?: AbortSignal) => client.deliveryResultRequest<Verifier>({ method: "GET", url: `/verifiers/${id(verifierID)}` }, { signal }),
    versions: (verifierID: string, cursor = "", signal?: AbortSignal) => client.deliveryResultRequest<RegistryPage<VerifierVersion>>({ method: "GET", url: `/verifiers/${id(verifierID)}/versions?${new URLSearchParams({limit: "50", ...(cursor ? {cursor: id(cursor)} : {})})}` }, { signal }),
    save: (definition: VerifierDefinition, existing: Verifier | null, key: string) => {
      const patch = {name: definition.name, state: definition.state, required: definition.required, applies_to: definition.applies_to, spec: definition.spec, timeout_ms: definition.timeout_ms, change_note: definition.change_note}
      return client.deliveryResultRequest<Verifier>({ method: existing ? "PATCH" : "POST", url: existing ? `/verifiers/${id(existing.id)}` : root }, { body: JSON.stringify(existing ? patch : definition), headers: { "Idempotency-Key": key, ...(existing ? {"If-Match": `"${existing.version}"`} : {}) } })
    },
  }
}
export const verifierTypes = ["TASK", "FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "RISK", "INCIDENT", "CONTENT"]
const lines = (value: unknown) => String(value ?? "").replace(/\r\n/g, "\n").split("\n").filter(value => value !== "")
const keys = (value: unknown) => String(value ?? "").split(/[\s,]+/).filter(Boolean)
export function verifierFormDefinition(values: Record<string, unknown>): VerifierDefinition {
  const layer = String(values.layer) as VerifierLayer
  const projectIDs = keys(values.project_ids).map(id)
  const spec: VerifierSpec = {}
  if (layer === "AUTOMATED") {
    spec.argv = lines(values.argv)
    spec.workdir = String(values.workdir ?? "") || undefined
    spec.report_format = String(values.report_format ?? "NONE")
    spec.report_globs = lines(values.report_globs)
    spec.stdout_tail_bytes = Number(values.stdout_tail_bytes ?? 8192)
    spec.tool_version_argv = lines(values.tool_version_argv)
    const env: Record<string, string> = {}
    for (const line of lines(values.env)) { const at = line.indexOf("="); if (at < 1 || !/^[A-Z_][A-Z0-9_]{0,63}$/.test(line.slice(0, at))) throw new Error("Invalid environment entry"); env[line.slice(0, at)] = line.slice(at + 1) }
    if (Object.keys(env).length) spec.env = env
  } else if (layer === "AI_ASSISTED") {
    spec.agent_id = id(String(values.agent_id ?? "")); spec.prompt_template = String(values.prompt_template ?? ""); spec.max_cost_micros = Number(values.max_cost_micros)
  } else if (layer === "HUMAN") {
    spec.instructions = String(values.instructions ?? ""); spec.eligible_role_keys = keys(values.eligible_role_keys)
  } else throw new Error("Invalid verifier layer")
  return { key: String(values.key ?? ""), name: String(values.name ?? ""), layer, state: values.state === "ARCHIVED" ? "ARCHIVED" : "ACTIVE", required: Boolean(values.required), applies_to: { work_item_types: Array.isArray(values.work_item_types) ? values.work_item_types.map(String) : [], risk_levels: Array.isArray(values.risk_levels) ? values.risk_levels.map(String) : [], project_ids: projectIDs.length ? projectIDs : null }, spec, timeout_ms: Number(values.timeout_ms), change_note: String(values.change_note ?? "") }
}
export function verifierFormValues(row: Verifier | null): Record<string, unknown> {
  const spec = row?.current.spec ?? {}
  return { key: row?.key ?? "", name: row?.name ?? "", layer: row?.layer ?? "AUTOMATED", state: row?.state ?? "ACTIVE", required: row?.required ?? false, work_item_types: row?.applies_to.work_item_types ?? [], risk_levels: row?.applies_to.risk_levels ?? [], project_ids: row?.applies_to.project_ids?.join(", ") ?? "", timeout_ms: row?.current.timeout_ms ?? 600000, change_note: "", argv: spec.argv?.join("\n") ?? "", workdir: spec.workdir ?? "", report_format: spec.report_format ?? "NONE", report_globs: spec.report_globs?.join("\n") ?? "", stdout_tail_bytes: spec.stdout_tail_bytes ?? 8192, tool_version_argv: spec.tool_version_argv?.join("\n") ?? "", env: Object.entries(spec.env ?? {}).map(([key, value]) => `${key}=${value}`).join("\n"), agent_id: spec.agent_id ?? "", prompt_template: spec.prompt_template ?? "", max_cost_micros: spec.max_cost_micros ?? 1000000, instructions: spec.instructions ?? "", eligible_role_keys: spec.eligible_role_keys?.join(", ") ?? "" }
}

export const verifierRegistryMessages = {
  "en-US": { title: "Verifier registry", hint: "Reusable versioned definitions. Policies match work type, risk and project; empty filters mean all. Definition or timeout changes append a version. Existing runs keep their frozen version. Registration is not a verification result. Only compatible checks already configured on work criteria run today; additional checks and AI or Human definitions are not scheduled automatically.", add: "New verifier", key: "Stable key", layer: "Layer", AUTOMATED: "Automated", AI_ASSISTED: "AI assisted", HUMAN: "Human", state: "State", ACTIVE: "Active", ARCHIVED: "Archived", required: "Required when matching", version: "Definition version", history: "Version history", workTypes: "Work types", risks: "Risk levels", projects: "Project IDs (comma separated; empty means all)", timeout: "Timeout (milliseconds)", note: "Change note", argv: "Command arguments (one literal argument per line; no shell)", workdir: "Relative working directory", env: "Environment (one NAME=value per line)", report: "Report format", globs: "Relative report patterns (one per line)", tail: "Output tail limit (bytes)", probe: "Version command arguments (one per line)", agent: "Reviewer Agent ID", prompt: "Review instructions template", cost: "Maximum cost (micros)", instructions: "Human verification instructions", roles: "Eligible role keys (comma separated)", empty: "No verifier definitions", invalid: "Check the definition and identifier fields.", saved: "Verifier saved", more: "Next page", first: "First page", all: "All", scope: "Policy filters", readonly: "You need policy_read to view verifier definitions.", editing: "Edit verifier", historyHint: "Versions are immutable. Archived definitions remain available in history and are excluded from new policy selections." },
  "zh-CN": { title: "验证器注册表", hint: "可复用的版本化定义。策略按工作类型、风险和项目匹配，空筛选表示全部。定义或超时变更会追加版本，已有运行保留冻结版本。登记不代表验证通过。目前只运行工作项标准中已配置且兼容的检查；新增检查及 AI 或人工定义不会自动调度。", add: "新增验证器", key: "稳定标识", layer: "验证层级", AUTOMATED: "自动化", AI_ASSISTED: "AI 辅助", HUMAN: "人工", state: "状态", ACTIVE: "启用", ARCHIVED: "归档", required: "匹配时必需", version: "定义版本", history: "版本历史", workTypes: "工作类型", risks: "风险级别", projects: "项目 ID（逗号分隔，留空表示全部）", timeout: "超时（毫秒）", note: "变更说明", argv: "命令参数（每行一个原始参数，不经 shell）", workdir: "相对工作目录", env: "环境变量（每行 NAME=value）", report: "报告格式", globs: "相对报告路径模式（每行一个）", tail: "输出尾部上限（字节）", probe: "版本命令参数（每行一个）", agent: "评审 Agent ID", prompt: "评审指令模板", cost: "费用上限（微单位）", instructions: "人工验证说明", roles: "允许的角色标识（逗号分隔）", empty: "暂无验证器定义", invalid: "请检查定义和标识字段。", saved: "验证器已保存", more: "下一页", first: "首页", all: "全部", scope: "策略筛选", readonly: "查看验证器定义需要 policy_read 权限。", editing: "编辑验证器", historyHint: "版本不可变。归档定义保留历史，但不再参与新的策略选择。" },
  "ms-MY": { title: "Daftar pengesah", hint: "Takrif berversi yang boleh digunakan semula. Dasar memadankan jenis kerja, risiko dan projek; penapis kosong bermaksud semua. Perubahan takrif atau had masa menambah versi. Larian sedia ada mengekalkan versi beku. Pendaftaran bukan keputusan pengesahan. Hanya semakan serasi yang telah dikonfigurasi pada kriteria kerja dijalankan; semakan tambahan serta takrif AI atau manusia belum dijadualkan secara automatik.", add: "Pengesah baharu", key: "Kunci tetap", layer: "Lapisan", AUTOMATED: "Automatik", AI_ASSISTED: "Bantuan AI", HUMAN: "Manusia", state: "Status", ACTIVE: "Aktif", ARCHIVED: "Diarkibkan", required: "Wajib apabila sepadan", version: "Versi takrif", history: "Sejarah versi", workTypes: "Jenis kerja", risks: "Tahap risiko", projects: "ID projek (pisahkan dengan koma; kosong untuk semua)", timeout: "Had masa (milisaat)", note: "Nota perubahan", argv: "Argumen arahan (satu argumen literal setiap baris; tanpa shell)", workdir: "Direktori kerja relatif", env: "Persekitaran (satu NAME=value setiap baris)", report: "Format laporan", globs: "Corak laporan relatif (satu setiap baris)", tail: "Had hujung output (bait)", probe: "Argumen arahan versi (satu setiap baris)", agent: "ID Agent penyemak", prompt: "Templat arahan semakan", cost: "Kos maksimum (mikro)", instructions: "Arahan pengesahan manusia", roles: "Kunci peranan layak (pisahkan dengan koma)", empty: "Tiada takrif pengesah", invalid: "Semak medan takrif dan pengenal.", saved: "Pengesah disimpan", more: "Halaman seterusnya", first: "Halaman pertama", all: "Semua", scope: "Penapis dasar", readonly: "Kebenaran policy_read diperlukan untuk melihat takrif.", editing: "Sunting pengesah", historyHint: "Versi tidak boleh diubah. Takrif diarkibkan kekal dalam sejarah dan dikecualikan daripada pemilihan dasar baharu." },
} as const
