// Manual acceptance fixture; never imported by the production router.
import { useState } from "react"
import { createRoot } from "react-dom/client"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import type { FormConfig } from "@workspace/ui/components/auto-form/types"
import { Button } from "@workspace/ui/components/button"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import messages from "@workspace/ui/i18n/messages/zh-CN.json"
import "../../src/index.css"

const fields: FormConfig = {
  name: {
    type: "text",
    name: "名称",
    required: true,
    config: {
      rules: [
        (value) =>
          String(value ?? "").length > 20 ? "名称最多 20 个字符" : undefined,
      ],
    },
  },
  password: { type: "password", name: "示例密码", config: { type: "password" } },
  amount: { type: "price", name: "金额（两位小数）", config: { decimals: 2 } },
  preciseAmount: {
    type: "price",
    name: "金额（三位小数）",
    config: { decimals: 3 },
  },
  role: {
    type: "select",
    name: "角色",
    config: {
      options: [
        { value: "reviewer", label: "评审人" },
        { value: "editor", label: "编辑者" },
      ],
    },
  },
  empty: { type: "text", name: "显式空值", value: "不应覆盖空值的默认内容" },
  tags: {
    type: "multi-select",
    name: "分类",
    config: { options: { design: "设计", engineering: "研发" } },
  },
  emptyTags: {
    type: "multi-select",
    name: "空多选",
    config: { options: { optional: "可选项" } },
  },
  dates: { type: "dateRange", name: "日期范围" },
}
const initialData = {
  name: "表单验收示例",
  password: "demo-only-password",
  amount: 1250,
  preciseAmount: 1250,
  role: "reviewer",
  empty: null,
  tags: ["design", "engineering"],
  emptyTags: [],
  dates: { start: "2026-10-15", end: "2026-10-18" },
}
export default function Fixture() {
  const [readonly, setReadonly] = useState(true)
  const [revision, setRevision] = useState(0)
  const [submissions, setSubmissions] = useState(0)
  const [status, setStatus] = useState("尚未提交")
  return (
    <main className="mx-auto max-w-4xl space-y-6 p-5 sm:p-8">
      <header className="space-y-2">
        <p className="text-sm text-muted-foreground">迁移验收 · M02 基础部分</p>
        <h1 className="text-2xl font-semibold">同一套表单，详情与编辑</h1>
        <p className="text-sm text-muted-foreground">
          本地示例，不保存业务数据。可检查密码遮蔽、金额精度、选项标签、日期范围与空值显示。
        </p>
      </header>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant={readonly ? "default" : "outline"}
          onClick={() => setReadonly(true)}
        >
          详情（只读）
        </Button>
        <Button
          variant={!readonly ? "default" : "outline"}
          onClick={() => setReadonly(false)}
        >
          编辑
        </Button>
        <Button
          variant="ghost"
          onClick={() => {
            setRevision((v) => v + 1)
            setSubmissions(0)
            setStatus("尚未提交")
          }}
        >
          重置示例
        </Button>
      </div>
      <section className="rounded-lg border p-5">
        <AutoForm
          key={revision}
          id="acceptance-form"
          fields={fields}
          initialData={initialData}
          mode="edit"
          readonly={readonly}
          onSubmit={async () => {
            setStatus("提交中…")
            await new Promise((resolve) => setTimeout(resolve, 1000))
            setSubmissions((count) => count + 1)
            setStatus("本地提交完成，未写入服务器")
          }}
        >
          <div className="flex flex-wrap items-center gap-3 md:col-span-2">
            <Button type="submit">
              {readonly ? "尝试提交只读表单" : "提交（可连续点击测试）"}
            </Button>
            <span className="text-sm text-muted-foreground">
              {readonly
                ? "点击后提交次数应保持不变。"
                : "清空名称测试必填；输入超过 20 字测试规则。"}
            </span>
          </div>
        </AutoForm>
      </section>
      <div role="status" className="text-sm">
        {status} · 成功提交次数：<strong>{submissions}</strong>
      </div>
    </main>
  )
}
createRoot(document.getElementById("root")!).render(
  <IntlAppProvider initialLocale="zh-CN" initialMessages={messages}>
    <Fixture />
  </IntlAppProvider>
)
