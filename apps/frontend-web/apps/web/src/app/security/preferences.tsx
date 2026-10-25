import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { useTheme } from "next-themes"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { notificationApi, type NotificationPreference } from "../../lib/notification-api"

// Keep workspace defaults in the notification owner's versioned resource.
function isWorkspacePreference(value: NotificationPreference, tenant: string) {
  return value.tenant_id === tenant && value.type_key === "*" && Number.isSafeInteger(value.version) && value.version >= 0
}

export function ProfilePreferences({ principal, tenant }: { principal: string; tenant: string }) {
  const t = useTranslations("iam")
  const { theme, setTheme } = useTheme()
  return <section className="space-y-5 border-b py-5" aria-label={t("profilePreferencesTitle")}>
    <h2 className="font-semibold">{t("profilePreferencesTitle")}</h2>
    <div className="grid gap-4 sm:grid-cols-2">
      <label className="grid gap-1 text-sm">{t("profilePreferencesTheme")}
        <select className="min-h-11 rounded-md border bg-background px-3" value={theme ?? "system"} onChange={event => setTheme(event.target.value)}>
          <option value="system">{t("profilePreferencesSystem")}</option><option value="light">{t("light")}</option><option value="dark">{t("dark")}</option>
        </select>
      </label>
      <div className="space-y-1"><p className="text-sm">{t("displayLanguage")}</p><LanguageSwitcher /></div>
    </div>
    {principal && tenant && tenant !== "0"
      ? <WorkspacePreferences key={`${principal}:${tenant}`} principal={principal} tenant={tenant} />
      : <p role="status" className="text-sm text-muted-foreground">{t("notificationChooseWorkspace")}</p>}
    <Link className="inline-flex min-h-11 items-center text-sm underline underline-offset-4" to="/user/notifications">{t("profilePreferencesTypes")}</Link>
  </section>
}

function WorkspacePreferences({ principal, tenant }: { principal: string; tenant: string }) {
  const t = useTranslations("iam")
  const [value, setValue] = useState<NotificationPreference | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState(false)
  const [revision, setRevision] = useState(0)
  const active = useRef(false)
  useEffect(() => {
    active.current = true
    return () => { active.current = false }
  }, [])
  useEffect(() => {
    let canceled = false
    void notificationApi.preference(principal, tenant).then(next => {
      if (canceled) return
      if (!isWorkspacePreference(next, tenant)) throw new Error(t("notificationLoadError"))
      setValue(next); setError(""); setLoaded(true)
    }).catch(() => {
      if (!canceled) { setValue(null); setError(t("notificationLoadError")); setLoaded(true) }
    })
    return () => { canceled = true }
  }, [principal, tenant, revision, t])
  const reload = () => { setValue(null); setLoaded(false); setError(""); setSaved(false); setRevision(n => n + 1) }
  const minute = (number: number) => `${String(Math.floor(number / 60)).padStart(2, "0")}:${String(number % 60).padStart(2, "0")}`
  const parseMinute = (text: string) => { const [hour = 0, min = 0] = text.split(":").map(Number); return hour * 60 + min }
  return <div className="space-y-3" aria-busy={!loaded || busy}>
    <h3 className="font-medium">{t("notificationSettings")}</h3>
    <p className="break-words text-sm text-muted-foreground">{t("notificationTenantSettings", { tenant })}</p>
    <p className="text-sm text-muted-foreground">{t("notificationInAppGuaranteed")}</p>
    {!loaded && <p role="status">{t("notificationLoading")}</p>}
    {error && <Alert>{error}</Alert>}
    {saved && <p role="status">{t("profilePreferencesSaved")}</p>}
    {loaded && !value && <Button variant="outline" onClick={reload}>{t("selfProfileReload")}</Button>}
    {value && <form className="space-y-4" onSubmit={event => {
      event.preventDefault()
      if (busy) return
      setBusy(true); setError(""); setSaved(false)
      void notificationApi.savePreference(principal, tenant, value).then(next => {
        if (!active.current) return
        if (!isWorkspacePreference(next, tenant)) throw new Error("scope mismatch")
        setValue(next); setSaved(true)
      }).catch(() => { if (active.current) setError(t("notificationSaveConflict")) })
        .finally(() => { if (active.current) setBusy(false) })
    }}>
      <fieldset disabled={busy} className="grid gap-4 sm:grid-cols-2">
        <label className="flex min-h-11 items-center gap-3 sm:col-span-2"><input type="checkbox" checked={value.quiet_enabled} onChange={event => setValue({ ...value, quiet_enabled: event.target.checked })} />{t("notificationQuiet")}</label>
        <label className="grid gap-1 text-sm">{t("notificationQuietStart")}<Input required type="time" value={minute(value.quiet_start_min)} onChange={event => setValue({ ...value, quiet_start_min: parseMinute(event.target.value) })} /></label>
        <label className="grid gap-1 text-sm">{t("notificationQuietEnd")}<Input required type="time" value={minute(value.quiet_end_min)} onChange={event => setValue({ ...value, quiet_end_min: parseMinute(event.target.value) })} /></label>
        <label className="grid gap-1 text-sm sm:col-span-2">{t("notificationTimezone")}<Input required maxLength={64} value={value.timezone} onChange={event => setValue({ ...value, timezone: event.target.value })} /></label>
        <label className="grid gap-1 text-sm">{t("notificationEmail")}<select className="min-h-11 rounded-md border bg-background px-3" value={value.email} onChange={event => setValue({ ...value, email: event.target.value as NotificationPreference["email"] })}>
          <option value="OFF">{t("notificationEmailOff")}</option><option value="IMMEDIATE">{t("notificationEmailImmediate")}</option><option value="DAILY_DIGEST">{t("notificationEmailDigest")}</option>
        </select></label>
        <label className="grid gap-1 text-sm">{t("notificationDigestHour")}<Input required type="number" min={0} max={23} value={value.digest_hour} onChange={event => setValue({ ...value, digest_hour: event.target.valueAsNumber })} /></label>
      </fieldset>
      <p className="text-sm text-muted-foreground">{t("notificationExternalAvailability")}</p>
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy}>{t("notificationSave")}</Button><Button type="button" variant="outline" disabled={busy} onClick={reload}>{t("selfProfileReload")}</Button></div>
    </form>}
  </div>
}
