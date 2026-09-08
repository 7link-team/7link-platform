import type { ReactNode } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"

export function Alert({ children }: { children: ReactNode }) {
  const t = useTranslations("iam")
  return (
    <div
      className="mb-4 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive"
      role="alert"
    >
      {typeof children === "string" && t.has(children) ? t(children) : children}
    </div>
  )
}
