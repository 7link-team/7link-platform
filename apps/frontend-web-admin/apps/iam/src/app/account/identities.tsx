import { useCallback, useEffect, useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Label } from "@workspace/ui/components/label"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAuth } from "../../components/auth"
import {
  iamApi,
  type IdentityLink,
  type IdentityLinkProvider,
} from "../../lib/iam-api"

type Pending = { purpose: "identity_link" | "identity_unlink"; target: string }
const pendingKey = "iam.identity-link-action"
export default function IdentitiesPage() {
  const t = useTranslations("iam")
  const { session } = useAuth()
  const [links, setLinks] = useState<IdentityLink[] | null>(null)
  const [providers, setProviders] = useState<IdentityLinkProvider[]>([])
  const [provider, setProvider] = useState("")
  const [error, setError] = useState("")
  const [reload, setReload] = useState(0)
  const [pending, setPending] = useState<Pending | null>(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")
      if (
        saved?.subject === session?.subject &&
        ["identity_link", "identity_unlink"].includes(saved.purpose) &&
        typeof saved.target === "string"
      )
        return saved
    } catch {
      /* Ignore invalid continuation data. */
    }
    sessionStorage.removeItem(pendingKey)
    return null
  })
  useEffect(() => {
    let active = true
    void Promise.all([
      iamApi.identityLinks(),
      iamApi.identityLinkProviders(),
    ]).then(
      ([next, available]) => {
        if (active) {
          setLinks(next)
          setProviders(available)
          setError("")
        }
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [reload])
  const begin = (action: Pending) => {
    sessionStorage.setItem(
      pendingKey,
      JSON.stringify({ ...action, subject: session?.subject })
    )
    setPending(action)
  }
  const complete = useCallback(
    async (proof: string) => {
      if (!pending) return
      if (pending.purpose === "identity_link") {
        const result = await iamApi.createIdentityLinkTransaction({
          provider_id: pending.target,
          proof,
          return_url: location.origin + location.pathname,
        })
        sessionStorage.removeItem(pendingKey)
        setPending(null)
        location.assign(result.authorization_url)
      } else {
        await iamApi.deleteIdentityLink(pending.target, proof)
        sessionStorage.removeItem(pendingKey)
        setPending(null)
        setLinks(null)
        setReload((value) => value + 1)
      }
    },
    [pending]
  )
  return (
    <>
      <PageHeader
        title={t("linkedIdentities")}
        description={t("linkedIdentitiesDescription")}
      />
      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!links ? (
        !error && <p role="status">{t("loading")}</p>
      ) : (
        <>
          {links.length === 0 ? (
            <p>{t("noLinkedIdentities")}</p>
          ) : (
            <ul className="space-y-3">
              {links.map((link) => (
                <li
                  key={link.id}
                  className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-4"
                >
                  <div className="min-w-0">
                    <h2 className="font-medium break-all">
                      {link.provider_name}
                    </h2>
                    <p className="text-sm break-all">
                      {link.display_name || link.external_subject}
                    </p>
                    <p className="text-sm break-all text-muted-foreground">
                      {link.tenant_id}
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    onClick={() =>
                      begin({ purpose: "identity_unlink", target: link.id })
                    }
                  >
                    {t("unlinkIdentity")}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <form
            className="mt-6 grid gap-3 rounded-md border p-4"
            onSubmit={(event) => {
              event.preventDefault()
              begin({ purpose: "identity_link", target: provider })
            }}
          >
            <Label htmlFor="identity-provider">{t("identityProvider")}</Label>
            <select
              id="identity-provider"
              required
              value={provider}
              onChange={(event) => setProvider(event.target.value)}
              className="min-h-11 rounded-md border bg-background px-3"
            >
              <option value="">{t("selectIdentityProvider")}</option>
              {providers.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.tenant_id}
                </option>
              ))}
            </select>
            {providers.length === 0 && <p>{t("noIdentityProviders")}</p>}
            <Button type="submit" disabled={!provider}>
              {t("linkIdentity")}
            </Button>
          </form>
        </>
      )}
      {pending && (
        <ReauthenticationDialog
          purpose={pending.purpose}
          target={pending.target}
          onVerified={complete}
          onClose={() => {
            sessionStorage.removeItem(pendingKey)
            setPending(null)
          }}
        />
      )}
    </>
  )
}
