export function historicalRate(value: number | null | undefined, unavailable: string) {
  return value == null ? unavailable : `${(value * 100).toFixed(1)}%`
}

export const metricsMessages = {
  "en-US": {
    history: "Historical facts", date: "UTC date", due: "Reports due", onTime: "Submitted on time", late: "Submitted late", pending: "Submission unavailable", timeliness: "Report timeliness", received: "Accepted replies", returned: "Returned replies", acceptance: "Delivery acceptance", passed: "Internal checks passed", failed: "Internal checks failed", verification: "Internal check pass rate",
    hint: "Reports use immutable first-submission receipts grouped by deadline; on-time rate divides on-time submissions by reports due. Delivery replies and completed internal verification attempts use their receipt dates. These are historical facts, not current work status or a suite verdict. Missing denominators are unavailable.",
    missing: "Reports without a persisted deadline excluded from timeliness:",
  },
  "zh-CN": {
    history: "历史事实", date: "UTC 日期", due: "到期报告", onTime: "按时提交", late: "逾期提交", pending: "缺少提交事实", timeliness: "报告及时率", received: "验收通过回执", returned: "退回回执", acceptance: "交付验收率", passed: "内部检查通过", failed: "内部检查失败", verification: "内部检查通过率",
    hint: "报告使用不可变的首次提交回执并按截止时间分组，及时率为按时提交数除以到期报告数。交付回执及已完成的内部验证尝试按回执日期统计。这些是历史事实，不代表当前工作状态或测试套件结论。缺少提交事实或分母时显示不可用。",
    missing: "因没有已保存截止时间而排除的报告：",
  },
  "ms-MY": {
    history: "Fakta sejarah", date: "Tarikh UTC", due: "Laporan perlu dihantar", onTime: "Dihantar tepat waktu", late: "Dihantar lewat", pending: "Fakta penyerahan tiada", timeliness: "Ketepatan waktu laporan", received: "Balasan diterima", returned: "Balasan dipulangkan", acceptance: "Penerimaan penghantaran", passed: "Semakan dalaman lulus", failed: "Semakan dalaman gagal", verification: "Kadar lulus semakan dalaman",
    hint: "Laporan menggunakan resit penyerahan pertama yang kekal mengikut tarikh akhir; kadar tepat waktu membahagi penyerahan tepat waktu dengan laporan yang perlu dihantar. Balasan penghantaran dan percubaan pengesahan dalaman lengkap menggunakan tarikh resit. Ini fakta sejarah, bukan status kerja semasa atau keputusan suite. Fakta atau penyebut yang tiada ditunjukkan sebagai tidak tersedia.",
    missing: "Laporan tanpa tarikh akhir tersimpan dikecualikan:",
  },
} as const
