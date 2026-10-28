import { useEffect, useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Switch } from "@workspace/ui/components/switch"
import { Label } from "@workspace/ui/components/label"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export default function SystemSettingsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const canManage = allowed("platform_administer")
  const [policy, setPolicy] = useState<{ enabled: boolean; available: boolean } | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    if (!canManage) return
    let active = true
    void iamApi.registrationPolicy().then(next => {
      if (active) { setPolicy(next); setEnabled(next.enabled); setError("") }
    }, (cause: Error) => { if (active) setError(cause.message) })
    return () => { active = false }
  }, [canManage, revision])
  if (!canManage) return <p>{t("noTokenPermissions")}</p>
  return (
    <section className="max-w-2xl">
      <PageHeader title={t("systemSettings")} description={t("registrationSettingsDescription")} />
      {error && <Alert>{error}</Alert>}
      {!policy ? <Button variant="outline" onClick={() => setRevision(value => value + 1)}>{error ? t("retry") : t("loading")}</Button> : (
        <form onSubmit={async event => {
          event.preventDefault()
          if (busy) return
          setBusy(true); setError(""); setSaved(false)
          try {
            const next = await iamApi.setRegistrationPolicy(enabled)
            setPolicy(next); setEnabled(next.enabled); setSaved(true)
          } catch (cause) { setError((cause as Error).message) }
          finally { setBusy(false) }
        }} className="space-y-5">
          <div className="flex items-start justify-between gap-6 border-y py-5">
            <div>
              <Label htmlFor="account-registration">{t("allowAccountRegistration")}</Label>
              <p id="registration-help" className="mt-2 text-sm text-muted-foreground">{t("accountRegistrationHelp")}</p>
            </div>
            <Switch id="account-registration" checked={enabled} disabled={busy} onCheckedChange={value => { setEnabled(value); setSaved(false) }} aria-describedby="registration-help" />
          </div>
          {!policy.available && <Alert>{t("registrationServiceUnavailable")}</Alert>}
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={busy || enabled === policy.enabled}>{busy ? t("submitting") : t("save")}</Button>
            {saved && <p role="status" className="text-sm text-muted-foreground">{t("registrationSettingsSaved")}</p>}
          </div>
        </form>
      )}
    </section>
  )
}
