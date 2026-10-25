export const homeMessages = {
  "en-US": {
    COST_PER_ACCEPTED: "Cost per accepted deliverable", FIRST_PASS_RATE: "First acceptance rate", ATTENTION_MIN_PER_ACCEPTED: "Human attention minutes / deliverable", REWORK_P50_HOURS: "Rework cycle", EVIDENCE_COVERAGE: "Evidence coverage", mean: "Mean (hours)", cycles: "closed cycles",
    health: "Project health", mode: "Mode", accepted: "Accepted", budget: "Budget (USD)", risk: "Risk", capacity: "Active / capacity", unavailable: "Unavailable", target: "Target ≥90%", facts: "Source numerator / denominator",
    WORK_ASSIGNED: "Executor assigned", WORK_CLAIMED: "Completion claimed", WORK_REWORK_REQUESTED: "Rework requested", WORK_ACCEPTED: "Work accepted", REPORT_CONFIRMED: "Report confirmed", DELEGATED_ACCEPTANCE: "Accepted through delegation", CLIENT_RECEIPT: "Client delivery receipt",
    NO_DENOMINATOR: "No qualifying records in this period", FIRST_ACCEPTANCE_HISTORY: "First acceptance history is unavailable", QUALITY_BRANCH_HISTORY: "Quality branch history is unavailable or not visible", HUMAN_ATTENTION_DURATION: "Source commands do not record Human attention duration", CLIENT_REJECTION_WORK_TIMELINE: "Complete client rejection and next acceptance timeline is unavailable", CLAIM_EVIDENCE_HISTORY: "Claim and evidence history is unavailable", EXECUTION_COST: "Complete execution cost is unavailable", CAPACITY_NOT_CONFIGURED_OR_NOT_VISIBLE: "Capacity is not configured or not visible",
  },
  "zh-CN": {
    COST_PER_ACCEPTED: "每个已接受交付物成本", FIRST_PASS_RATE: "首次验收率", ATTENTION_MIN_PER_ACCEPTED: "人工关注分钟 / 交付物", REWORK_P50_HOURS: "返工周期", EVIDENCE_COVERAGE: "证据覆盖率", mean: "均值（小时）", cycles: "次闭合周期",
    health: "项目健康", mode: "模式", accepted: "已接受", budget: "预算（美元）", risk: "风险", capacity: "活跃 / 容量", unavailable: "不可用", target: "目标 ≥90%", facts: "来源分子 / 分母",
    WORK_ASSIGNED: "已分派执行者", WORK_CLAIMED: "已提交完成声明", WORK_REWORK_REQUESTED: "已要求返工", WORK_ACCEPTED: "工作项已接受", REPORT_CONFIRMED: "报告已确认", DELEGATED_ACCEPTANCE: "经委托接受", CLIENT_RECEIPT: "甲方验收回执",
    NO_DENOMINATOR: "本周期暂无符合条件的记录", FIRST_ACCEPTANCE_HISTORY: "首次接受历史不可用", QUALITY_BRANCH_HISTORY: "质量分支历史不可用或不可见", HUMAN_ATTENTION_DURATION: "来源命令未记录人工关注时长", CLIENT_REJECTION_WORK_TIMELINE: "完整的甲方拒收及后续接受时间线不可用", CLAIM_EVIDENCE_HISTORY: "完成声明与证据历史不可用", EXECUTION_COST: "完整执行成本不可用", CAPACITY_NOT_CONFIGURED_OR_NOT_VISIBLE: "容量未配置或不可见",
  },
  "ms-MY": {
    COST_PER_ACCEPTED: "Kos setiap hasil diterima", FIRST_PASS_RATE: "Kadar penerimaan pertama", ATTENTION_MIN_PER_ACCEPTED: "Minit perhatian manusia / hasil", REWORK_P50_HOURS: "Kitaran kerja semula", EVIDENCE_COVERAGE: "Liputan bukti", mean: "Purata (jam)", cycles: "kitaran tertutup",
    health: "Kesihatan projek", mode: "Mod", accepted: "Diterima", budget: "Bajet (USD)", risk: "Risiko", capacity: "Aktif / kapasiti", unavailable: "Tidak tersedia", target: "Sasaran ≥90%", facts: "Pengangka / penyebut sumber",
    WORK_ASSIGNED: "Pelaksana ditugaskan", WORK_CLAIMED: "Penyiapan diisytiharkan", WORK_REWORK_REQUESTED: "Kerja semula diminta", WORK_ACCEPTED: "Kerja diterima", REPORT_CONFIRMED: "Laporan disahkan", DELEGATED_ACCEPTANCE: "Diterima melalui delegasi", CLIENT_RECEIPT: "Resit penghantaran pelanggan",
    NO_DENOMINATOR: "Tiada rekod layak dalam tempoh ini", FIRST_ACCEPTANCE_HISTORY: "Sejarah penerimaan pertama tidak tersedia", QUALITY_BRANCH_HISTORY: "Sejarah cabang kualiti tidak tersedia atau tidak kelihatan", HUMAN_ATTENTION_DURATION: "Arahan sumber tidak merekod tempoh perhatian manusia", CLIENT_REJECTION_WORK_TIMELINE: "Garis masa lengkap penolakan pelanggan dan penerimaan seterusnya tidak tersedia", CLAIM_EVIDENCE_HISTORY: "Sejarah pengisytiharan dan bukti tidak tersedia", EXECUTION_COST: "Kos pelaksanaan lengkap tidak tersedia", CAPACITY_NOT_CONFIGURED_OR_NOT_VISIBLE: "Kapasiti tidak dikonfigurasi atau tidak kelihatan",
  },
} as const

export function homeMetricValue(key: string, value: string | null | undefined, unknown: string) {
  if (value == null) return unknown
  if (key === "FIRST_PASS_RATE" || key === "EVIDENCE_COVERAGE") return `${(Number(value) * 100).toFixed(1)}%`
  if (key === "COST_PER_ACCEPTED") {
    // Backend ratio is exact decimal micros; round to cents without passing
    // int64 currency numerators through a JavaScript floating point number.
    const [whole = "0"] = value.split(".")
    const cents = (BigInt(whole) + 5000n) / 10000n
    return `$${cents / 100n}.${String(cents % 100n).padStart(2, "0")}`
  }
  return value
}
