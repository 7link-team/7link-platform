import { useCallback, useEffect, useState } from "react"
import { LogOut, Eye } from "lucide-react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { exitAccountView, type ActiveAccountView } from "../lib/account-view"

export function AccountViewBanner({ view }: { view: ActiveAccountView }) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const close = useCallback(async () => {
    setBusy(true)
    setError("")
    try {
      await exitAccountView()
    } catch {
      setError(t("accountViewExitFailed"))
      setBusy(false)
    }
  }, [t])
  useEffect(() => {
    const expire = () => {
      void close()
    }
    const timer = window.setTimeout(
      expire,
      Math.max(0, Date.parse(view.expires_at) - Date.now())
    )
    window.addEventListener("account-view-expired", expire)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener("account-view-expired", expire)
    }
  }, [view.id, view.expires_at, close])
  return (
    <div
      role="status"
      className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-amber-500 bg-amber-50 px-4 py-2 text-sm text-zinc-900 dark:bg-amber-950 dark:text-zinc-100"
    >
      <span className="flex min-w-0 flex-wrap items-center gap-2">
        <Eye className="size-4 shrink-0" />
        {t("accountView")} · {view.principal_id} ·{" "}
        {t(
          view.mode === "read_only"
            ? "accountViewReadOnly"
            : "accountViewWritable"
        )}
      </span>
      {error && <span role="alert">{error}</span>}
      <Button
        size="sm"
        variant="outline"
        disabled={busy}
        onClick={() => {
          void close()
        }}
      >
        <LogOut className="size-4" />
        {t("accountViewExit")}
      </Button>
    </div>
  )
}
