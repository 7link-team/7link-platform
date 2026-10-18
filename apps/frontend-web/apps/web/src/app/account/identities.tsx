import { AutoTable } from "@workspace/ui/components/auto-table"
import { useCallback, useEffect, useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { AutoForm } from "@workspace/ui/components/auto-form"
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
  const [readLoading, setReadLoading] = useState(true)
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
    void Promise.all([iamApi.identityLinks(), iamApi.identityLinkProviders()])
      .then(
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
      .finally(() => {
        if (active) setReadLoading(false)
      })
    return () => {
      active = false
    }
  }, [reload])
  const updateProvider = useCallback(
    (values: Record<string, unknown>) =>
      setProvider(String(values.provider_id ?? "")),
    []
  )

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
    <div className="flex min-h-0 flex-1 flex-col gap-4">
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
      <AutoTable
        fill
        toolbar={
          <AutoForm
            className="w-full items-end md:grid-cols-[minmax(12rem,1fr)_auto]"
            fields={{
              provider_id: {
                name: t("identityProvider"),
                type: "select",
                required: true,
                value: provider,
                config: {
                  id: "identity-provider",
                  placeholder: t("selectIdentityProvider"),
                  options: providers.map((item) => ({
                    value: item.id,
                    label: `${item.name} · ${item.tenant_id}`,
                  })),
                },
              },
            }}
            onChange={updateProvider}
            onSubmit={(values) =>
              begin({
                purpose: "identity_link",
                target: String(values.provider_id),
              })
            }
          >
            {providers.length === 0 && <p>{t("noIdentityProviders")}</p>}
            <Button type="submit" disabled={!provider}>
              {t("linkIdentity")}
            </Button>
          </AutoForm>
        }
        onRefresh={() => {
          setReadLoading(true)
          setReload((value) => value + 1)
        }}
        data={links ?? []}
        loading={readLoading || (!links && !error)}
        emptyContent={t("noLinkedIdentities")}
        rowKey="id"
        searchable
        columns={[
          {
            field: "provider_name",
            name: t("identityProvider"),
            render: (_, link) => (
              <h2 className="font-medium break-all">{link.provider_name}</h2>
            ),
          },
          {
            field: "display_name",
            name: t("name"),
            render: (_, link) => (
              <span className="break-all">
                {link.display_name || link.external_subject}
              </span>
            ),
          },
          {
            field: "tenant_id",
            name: t("tenant"),
            render: (_, link) => (
              <span className="break-all">{link.tenant_id}</span>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: t("actions"),
            render: (_, link) => (
              <Button
                variant="outline"
                onClick={() =>
                  begin({ purpose: "identity_unlink", target: link.id })
                }
              >
                {t("unlinkIdentity")}
              </Button>
            ),
          },
        ]}
      />
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
    </div>
  )
}
