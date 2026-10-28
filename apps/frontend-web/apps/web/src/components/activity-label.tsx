import { useTranslations } from "@workspace/ui/i18n/intl-provider"

/** Presentation comes from the declaring backend module, including its locale. */
export function ActivityLabel({
  code,
  display_name,
  description,
  module,
}: {
  code: string
  display_name?: string
  description?: string
  module?: string
}) {
  const t = useTranslations("iam")
  return (
    <div className="min-w-48 space-y-1 whitespace-normal">
      <p className="font-medium">{display_name || t("activityUnregistered")}</p>
      {description && (
        <p className="text-sm text-muted-foreground">{description}</p>
      )}
      <p className="text-xs break-all text-muted-foreground">
        {code}
        {module ? ` · ${t("activityModule")}: ${module}` : ""}
      </p>
    </div>
  )
}
