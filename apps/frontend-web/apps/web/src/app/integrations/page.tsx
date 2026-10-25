import { useEffect, useRef, useState } from "react"
import { useOutletContext, useParams } from "react-router"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import type { OperationRequestContract } from "@workspace/ui/components/auto-form/contract"
import { Button } from "@workspace/ui/components/button"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import {
  connectionPath,
  integrationsApi,
  mappingInput,
  mappingPath,
  type Connection,
  type Mapping,
  type Page,
} from "./api"
import { integrationMessages } from "./messages"
import { IntegrationObservations } from "./observations"
type Labels = { [K in keyof (typeof integrationMessages)["en-US"]]: string }
function useLabels() {
  const locale = useLocale()
  return (
    integrationMessages[locale as keyof typeof integrationMessages] ??
    integrationMessages["en-US"]
  )
}
export default function IntegrationsPage() {
  const { tenantID = "" } = useParams()
  const context = useOutletContext<{
    permissions?: Set<string>
    permissionsResolved?: boolean
  }>()
  const labels = useLabels()
  if (!context?.permissionsResolved)
    return <p role="status">{labels.loading}</p>
  if (!context.permissions?.has("integration_manage"))
    return <Alert>{labels.forbidden}</Alert>
  return <IntegrationSettings key={tenantID} tenant={tenantID} />
}
function IntegrationSettings({ tenant }: { tenant: string }) {
  const labels = useLabels()
  const [revision, setRevision] = useState(0)
  const [cursor, setCursor] = useState("")
  const [history, setHistory] = useState<string[]>([])
  const [page, setPage] = useState<Page<Connection>>({
    items: [],
    has_more: false,
  })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [selected, setSelected] = useState<Connection | null>(null)
  const [editing, setEditing] = useState<Connection | "new" | null>(null)
  const [checking, setChecking] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    void integrationsApi
      .list<Connection>(connectionPath(tenant), cursor, controller.signal)
      .then(
        (result) => {
          if (!controller.signal.aborted) {
            setPage(result)
            setError("")
            setLoading(false)
          }
        },
        (cause) => {
          if (!controller.signal.aborted) {
            setError(String(cause instanceof Error ? cause.message : cause))
            setPage({ items: [], has_more: false })
            setLoading(false)
          }
        }
      )
    return () => controller.abort()
  }, [tenant, cursor, revision])
  const reload = () => {
    setLoading(true)
    setRevision((value) => value + 1)
  }
  return (
    <main className="space-y-5">
      <PageHeader
        title={labels.title}
        description={labels.description}
        action={<Button onClick={() => setEditing("new")}>{labels.add}</Button>}
      />
      <p
        role="status"
        className="border-l-2 border-amber-500 bg-muted/40 p-4 text-sm leading-relaxed"
      >
        {labels.unavailable}
      </p>
      {error && <Alert>{error}</Alert>}
      {notice && <p role="status">{notice}</p>}
      <Button variant="outline" onClick={reload} disabled={loading}>
        {labels.refresh}
      </Button>
      {loading ? (
        <p role="status">{labels.loading}</p>
      ) : page.items.length === 0 ? (
        <p>{labels.empty}</p>
      ) : (
        <ul className="divide-y border-y">
          {page.items.map((connection) => (
            <li
              key={connection.id}
              className="flex flex-wrap items-center justify-between gap-4 py-4"
            >
              <div>
                <button
                  type="button"
                  className="text-left font-medium underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2"
                  onClick={() => {
                    setSelected(connection)
                    setError("")
                  }}
                >
                  {connection.name}
                </button>
                <p className="mt-1 text-sm text-muted-foreground">
                  {labels.state}: {connection.state} ? {labels.last4}:{" "}
                  {connection.token_last4 || labels.missing}
                </p>
              </div>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  onClick={() => setEditing(connection)}
                >
                  {labels.edit}
                </Button>
                <Button
                  variant="outline"
                  disabled={checking}
                  onClick={async () => {
                    setChecking(true)
                    setError("")
                    try {
                      await integrationsApi.check(tenant, connection.id)
                    } catch (cause) {
                      setError(
                        cause instanceof Error ? cause.message : String(cause)
                      )
                    } finally {
                      setChecking(false)
                    }
                  }}
                >
                  {labels.check}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={!history.length || loading}
          onClick={() => {
            setCursor(history.at(-1) ?? "")
            setHistory(history.slice(0, -1))
            setLoading(true)
          }}
        >
          {labels.back}
        </Button>
        <Button
          variant="outline"
          disabled={!page.has_more || loading}
          onClick={() => {
            setHistory([...history, cursor])
            setCursor(page.next_cursor ?? "")
            setLoading(true)
          }}
        >
          {labels.next}
        </Button>
      </div>
      {editing && (
        <ConnectionForm
          key={editing === "new" ? "new" : editing.id}
          tenant={tenant}
          connection={editing === "new" ? null : editing}
          labels={labels}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null)
            setNotice(labels.success)
            reload()
          }}
        />
      )}
      {selected && (
        <MappingSettings
          key={selected.id}
          tenant={tenant}
          connection={selected}
          labels={labels}
        />
      )}
    </main>
  )
}
function MetadataForm({
  url,
  method,
  fields,
  initialData,
  labels,
  onSubmit,
}: {
  url: string
  method: "POST" | "PATCH"
  fields: Record<string, FieldConfig>
  initialData?: Record<string, unknown>
  labels: Labels
  onSubmit: (values: Record<string, unknown>, key: string) => Promise<void>
}) {
  const [contract, setContract] = useState<OperationRequestContract | null>(
    null
  )
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void integrationsApi.contract(url, method, controller.signal).then(
      (value) => {
        if (!controller.signal.aborted) {
          setContract(value)
          setError("")
        }
      },
      (cause) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : String(cause))
      }
    )
    return () => controller.abort()
  }, [url, method, revision])
  return (
    <>
      {error && <Alert>{error}</Alert>}
      {!contract && (
        <Button
          variant="outline"
          onClick={() => setRevision((value) => value + 1)}
        >
          {labels.retry}
        </Button>
      )}
      <AutoForm
        contract={contract}
        fields={fields}
        initialData={initialData}
        mode={method === "PATCH" ? "edit" : "add"}
        onSubmit={async (values) => {
          const body = JSON.stringify(values)
          const key =
            attempt.current?.body === body
              ? attempt.current.key
              : crypto.randomUUID()
          attempt.current = { body, key }
          try {
            await onSubmit(values, key)
            attempt.current = null
          } catch (cause) {
            setError(cause instanceof Error ? cause.message : String(cause))
            throw cause
          }
        }}
      >
        <Button type="submit" disabled={!contract}>
          {labels.save}
        </Button>
      </AutoForm>
    </>
  )
}
function ConnectionForm({
  tenant,
  connection,
  labels,
  onClose,
  onDone,
}: {
  tenant: string
  connection: Connection | null
  labels: Labels
  onClose: () => void
  onDone: () => void
}) {
  const [pending, setPending] = useState<{
    values: Record<string, unknown>
    key: string
  } | null>(null)
  const url = `${connectionPath(tenant)}${connection ? `/${encodeURIComponent(connection.id)}` : ""}`
  const fields: Record<string, FieldConfig> = {
    name: { name: labels.name, type: "text" },
    organization_id: { name: labels.org, type: "text" },
    api_endpoint: { name: labels.endpoint, type: "text" },
    token: {
      name: labels.token,
      type: "password",
      config: { autoComplete: "new-password" },
    },
    webhook_secret: {
      name: labels.webhook,
      type: "password",
      config: { autoComplete: "new-password" },
    },
    disabled: { name: labels.disabled, type: "checkbox", value: false },
    reauth_proof: { name: "", type: "text", show: false, submit: false },
  }
  return (
    <section
      aria-label={connection ? labels.edit : labels.add}
      className="space-y-4 border-t pt-5"
    >
      <h2 className="font-semibold">{connection ? labels.edit : labels.add}</h2>
      <p className="text-sm text-muted-foreground">{labels.credentials}</p>
      <MetadataForm
        url={url}
        method={connection ? "PATCH" : "POST"}
        labels={labels}
        fields={fields}
        initialData={
          connection
            ? {
                ...connection,
                disabled: connection.state === "DISABLED",
                token: "",
                webhook_secret: "",
              }
            : undefined
        }
        onSubmit={async (values, key) => setPending({ values, key })}
      />
      <Button variant="outline" onClick={onClose}>
        {labels.cancel}
      </Button>
      {pending && (
        <ReauthenticationDialog
          purpose="integration_credentials"
          target={`tenant:${tenant}:integration:${connection?.id ?? "new"}`}
          onClose={() => setPending(null)}
          onVerified={async (proof) => {
            await integrationsApi.save<Connection>(
              url,
              connection ? "PATCH" : "POST",
              { ...pending.values, reauth_proof: proof },
              pending.key,
              connection?.version
            )
            setPending(null)
            onDone()
          }}
        />
      )}
    </section>
  )
}
function MappingSettings({
  tenant,
  connection,
  labels,
}: {
  tenant: string
  connection: Connection
  labels: Labels
}) {
  const [page, setPage] = useState<Page<Mapping>>({
    items: [],
    has_more: false,
  })
  const [cursor, setCursor] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [editing, setEditing] = useState<Mapping | "new" | null>(null)
  const base = mappingPath(tenant, connection.id)
  useEffect(() => {
    const controller = new AbortController()
    void integrationsApi.list<Mapping>(base, cursor, controller.signal).then(
      (value) => {
        if (!controller.signal.aborted) {
          setPage(value)
          setLoading(false)
          setError("")
        }
      },
      (cause) => {
        if (!controller.signal.aborted) {
          setPage({ items: [], has_more: false })
          setLoading(false)
          setError(cause instanceof Error ? cause.message : String(cause))
        }
      }
    )
    return () => controller.abort()
  }, [base, cursor, revision])
  const existing = editing && editing !== "new" ? editing : null
  const fields: Record<string, FieldConfig> = {
    external_project_id: { name: labels.external, type: "text" },
    external_project_name: { name: labels.externalName, type: "text" },
    project_id: { name: labels.project, type: "text", disabled: !!existing },
    sync_mode: {
      name: labels.mode,
      type: "select",
      value: "IMPORT_ONLY",
      config: {
        options: [
          { value: "IMPORT_ONLY", label: labels.importOnly },
          { value: "READ_MIRROR", label: labels.mirror },
          { value: "BIDIRECTIONAL", label: labels.bidirectional },
        ],
      },
    },
    field_masters: { name: labels.masters, type: "textarea", value: "{}" },
    paused: { name: labels.paused, type: "checkbox", value: false },
    codeup_repositories: { name: "", type: "text", show: false, submit: false },
    trusted_pipelines: { name: "", type: "text", show: false, submit: false },
  }
  return (
    <section className="space-y-4 border-t pt-5">
      <div className="flex flex-wrap justify-between gap-3">
        <h2 className="font-semibold">
          {labels.mappings} ? {connection.name}
        </h2>
        <Button variant="outline" onClick={() => setEditing("new")}>
          {labels.map}
        </Button>
      </div>
      {error && <Alert>{error}</Alert>}
      {loading ? (
        <p role="status">{labels.loading}</p>
      ) : page.items.length === 0 ? (
        <p>{labels.noMappings}</p>
      ) : (
        <ul className="divide-y">
          {page.items.map((mapping) => (
            <li
              key={mapping.id}
              className="flex flex-wrap items-center justify-between gap-3 py-3"
            >
              <div>
                <p>
                  {mapping.external_project_name} ? {mapping.project_id}
                </p>
                <p className="text-sm text-muted-foreground">
                  {mapping.sync_mode}
                  {mapping.paused ? ` ? ${labels.paused}` : ""}
                </p>
              </div>
              <Button variant="outline" onClick={() => setEditing(mapping)}>
                {labels.edit}
              </Button>
              <div className="w-full min-w-0">
                <IntegrationObservations
                  key={`${tenant}:${connection.id}:${mapping.id}`}
                  tenant={tenant}
                  connection={connection.id}
                  mapping={mapping.id}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={!cursor || loading}
          onClick={() => {
            setCursor("")
            setLoading(true)
          }}
        >
          {labels.back}
        </Button>
        <Button
          variant="outline"
          disabled={!page.has_more || loading}
          onClick={() => {
            setCursor(page.next_cursor ?? "")
            setLoading(true)
          }}
        >
          {labels.next}
        </Button>
      </div>
      {editing && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">{labels.masterHelp}</p>
          <MetadataForm
            key={existing?.id ?? "new"}
            labels={labels}
            url={`${base}${existing ? `/${encodeURIComponent(existing.id)}` : ""}`}
            method={existing ? "PATCH" : "POST"}
            fields={fields}
            initialData={
              existing
                ? {
                    ...existing,
                    field_masters: JSON.stringify(existing.field_masters),
                  }
                : undefined
            }
            onSubmit={async (values, key) => {
              await integrationsApi.save<Mapping>(
                `${base}${existing ? `/${encodeURIComponent(existing.id)}` : ""}`,
                existing ? "PATCH" : "POST",
                mappingInput({
                  ...values,
                  codeup_repositories: existing?.codeup_repositories ?? [],
                  trusted_pipelines: existing?.trusted_pipelines ?? [],
                }),
                key,
                existing?.version
              )
              setEditing(null)
              setLoading(true)
              setRevision((value) => value + 1)
            }}
          />
          <Button variant="outline" onClick={() => setEditing(null)}>
            {labels.cancel}
          </Button>
        </div>
      )}
    </section>
  )
}
