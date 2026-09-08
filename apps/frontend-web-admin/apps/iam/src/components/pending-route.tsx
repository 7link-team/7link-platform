import { useTranslations } from "@workspace/ui/i18n/intl-provider"
export function PendingRoute() {
  const t = useTranslations("iam")

  return (
    <div
      className="grid min-h-svh place-items-center bg-background text-sm text-muted-foreground"
      aria-busy="true"
      aria-live="polite"
    >
      {t("loading")}
    </div>
  )
}
