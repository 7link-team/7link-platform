import { useEffect, useMemo, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { ApiError } from "../../lib/iam-api"
import { useRequestContract } from "../../lib/use-request-contract"
import { selfProfileApi, selfProfileRoute, type SelfProfile, type SelfProfilePatch } from "./profile-api"

export function ProfileEditor({ principal }: { principal: string }) {
  const t = useTranslations("iam")
  const auth = useAuth()
  const route = useMemo(() => selfProfileRoute(principal), [principal])
  const metadata = useRequestContract(route)
  const [profile, setProfile] = useState<SelfProfile | null>(null)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void selfProfileApi.get(principal, controller.signal).then(value => {
      if (!controller.signal.aborted) { setProfile(value); setError("") }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setProfile(null); setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
    })
    return () => controller.abort()
  }, [principal, revision, t])
  return <section className="space-y-3 border-b py-5" aria-label={t("selfProfileTitle")}>
    <h2 className="font-semibold">{t("selfProfileTitle")}</h2>
    <p className="text-sm text-muted-foreground">{t("selfProfileContactHint")}</p>
    {error && <Alert>{error}</Alert>}
    {saved && <p role="status">{t("selfProfileSaved")}</p>}
    {metadata.failed && <Alert>{t("selfProfileContractFailed")}</Alert>}
    {(!profile || !metadata.contract) && <Button variant="outline" onClick={() => { setRevision(value => value + 1); metadata.retry() }}>{t("selfProfileReload")}</Button>}
    {profile && metadata.contract && <AutoForm key={JSON.stringify(profile)} contract={metadata.contract}
      fields={{
        display_name: { type: "text", name: t("selfProfileNickname"), value: profile.display_name, disabled: busy },
        email: { type: "text", name: t("primaryEmail"), value: profile.email, disabled: busy },
        phone: { type: "text", name: t("selfProfileContact"), value: profile.phone, disabled: busy },
      }}
      onSubmit={async values => {
        const body: SelfProfilePatch = {}
        if (String(values.display_name ?? "") !== profile.display_name) body.display_name = String(values.display_name ?? "")
        if (String(values.email ?? "") !== profile.email) body.email = String(values.email ?? "")
        if (String(values.phone ?? "") !== profile.phone) body.phone = String(values.phone ?? "")
        if (!Object.keys(body).length) return
        setBusy(true); setError(""); setSaved(false)
        try {
          const updated = await selfProfileApi.update(principal, body)
          setProfile(updated); setSaved(true)
          try { await auth.refresh() } catch (cause) {
            if (!(cause instanceof ApiError && cause.status === 401)) throw cause
          }
        } catch (cause) {
          setError(cause instanceof Error ? cause.message : t("clientSaveFailed"))
          throw cause
        } finally { setBusy(false) }
      }}><Button type="submit" disabled={busy}>{busy ? t("loading") : t("selfProfileSave")}</Button></AutoForm>}
  </section>
}
