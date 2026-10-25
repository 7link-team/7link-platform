import { useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import {
  iamApi,
  type KnowledgeEntry,
  type KnowledgeQuery,
  type KnowledgeWrite,
  type KnowledgeReview,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { MetricsHistory } from "./metrics-history"
import { NativeSearch } from "./native-search"
import { useRequestContract } from "../../lib/use-request-contract"

export default function KnowledgePage() {
  const { tenantID = "", entityID = "" } = useParams()
  return (
    <Knowledge
      key={`${tenantID}:${entityID}`}
      tenantID={tenantID}
      entityID={entityID}
    />
  )
}

export function MemoryReviewPage() {
  const {tenantID = "", entityID: routeProject = ""} = useParams()
  const [search] = useSearchParams()
  const project = routeProject || search.get("project_id") || ""
  const entityID = /^[1-9][0-9]{0,18}$/.test(project) ? project : ""
  return <Knowledge key={`${tenantID}:${entityID}:review:${search}`} tenantID={tenantID} entityID={entityID} review/>
}

function Knowledge({
  tenantID,
  entityID,
  review = false,
}: {
  tenantID: string
  entityID: string
  review?: boolean
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [search] = useSearchParams()
  const exactEntry = review && /^[1-9][0-9]{0,18}$/.test(search.get("entry_id") ?? "") ? search.get("entry_id")! : ""
  const initialAgent = review && search.get("scope") === "AGENT" && /^[1-9][0-9]{0,18}$/.test(search.get("agent_principal_id") ?? "") ? search.get("agent_principal_id")! : ""
  const [scope, setScope] = useState<"TEAM" | "PROJECT" | "AGENT">(
    initialAgent ? "AGENT" : entityID ? "PROJECT" : "TEAM"
  )
  const [agentDraft, setAgentDraft] = useState(initialAgent)
  const [agent, setAgent] = useState(initialAgent)
  const [state, setState] = useState<KnowledgeQuery["state"]>(review ? "PENDING_REVIEW" : "ACTIVE")
  const [loadedItems, setItems] = useState<KnowledgeEntry[]>([])
  const [loadedCursor, setCursor] = useState("")
  const [loadedSelected, setSelected] = useState<KnowledgeEntry | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState("")
  const [subject, setSubject] = useState("")
  useEffect(() => {
    let active = true
    void iamApi.session().then((session) => {
      if (active) setSubject(session.subject)
    }).catch((e: Error) => {
      if (active) setError(e.message)
    })
    return () => { active = false }
  }, [tenantID])
  const [loadingMore, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const selection = `${scope}:${agent}:${state}:${revision}`
  const [resultKey, setResultKey] = useState("")
  const items = resultKey === selection ? loadedItems : []
  const cursor = resultKey === selection ? loadedCursor : ""
  const selected = resultKey === selection ? loadedSelected : null
  const loading =
    loadingMore ||
    (resultKey !== selection && (scope !== "AGENT" || Boolean(agent)))
  const current = useRef(selection)
  useEffect(() => {
    current.current = selection
  }, [selection])
  const query = useMemo<KnowledgeQuery>(
    () => ({
      scope,
      state,
      limit: 50,
      project_id: scope !== "TEAM" ? entityID || undefined : undefined,
      agent_principal_id: scope === "AGENT" ? agent : undefined,
    }),
    [scope, state, entityID, agent]
  )
  // TEAM scope has no project owner. AGENT may be tenant or project scoped.
  useEffect(() => {
    if (scope === "AGENT" && !agent) return
    const controller = new AbortController()
    let entry: KnowledgeEntry | null = null
    void (async () => {
          if (exactEntry) {
            entry = await iamApi.knowledgeEntry(tenantID, exactEntry, controller.signal)
            if (entry.scope !== scope || (entry.project_id ?? "") !== (scope === "TEAM" ? "" : entityID) || (entry.agent_principal_id ?? "") !== (scope === "AGENT" ? agent : "")) entry = null
            if (controller.signal.aborted) return
            setResultKey(selection)
            setSelected(entry)
            setItems(entry ? [entry] : [])
          }
          const page = await iamApi.knowledgeEntries(tenantID, query, controller.signal)
          if (controller.signal.aborted) return
          setResultKey(selection)
          setSelected(entry)
          setItems(entry && !page.items?.some((row) => row.id === entry?.id) ? [entry, ...(page.items ?? [])] : page.items ?? [])
          setCursor(page.next_cursor ?? "")
          setError("")
        })().catch(
        (e: Error) => {
          if (!controller.signal.aborted) {
            setResultKey(selection)
            setSelected(entry)
            setItems(entry ? [entry] : [])
            setCursor("")
            setError(e.message)
          }
        }
      )
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [tenantID, scope, agent, query, revision, selection, exactEntry, entityID])
  const more = async () => {
    const key = selection
    setLoading(true)
    try {
      const page = await iamApi.knowledgeEntries(tenantID, { ...query, cursor })
      if (current.current !== key) return
      setItems((old) => [
        ...old,
        ...(page.items ?? []).filter(
          (row) => !old.some((item) => item.id === row.id)
        ),
      ])
      setCursor(page.next_cursor ?? "")
    } catch (e) {
      if (current.current === key) setError((e as Error).message)
    } finally {
      if (current.current === key) setLoading(false)
    }
  }
  const refresh = () => {
    setCreating(false)
    setSelected(null)
    setRevision((n) => n + 1)
  }
  const writeScope = {
    scope,
    ...(scope !== "TEAM" && entityID ? { project_id: entityID } : {}),
    ...(scope === "AGENT" ? { agent_principal_id: agent } : {}),
  }
  return (
    <div className="space-y-4">
      {!review && <nav aria-label={t("knowledge")} className="flex flex-wrap gap-3"><Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenantID)}${entityID ? `/entities/${encodeURIComponent(entityID)}` : ""}/knowledge/sources`}>{t("knowledgeSources")}</Link><Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenantID)}/knowledge/skills`}>{t("knowledgeSkills")}</Link>{allowed("knowledge_manage") && <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenantID)}/knowledge/settings`}>{t("knowledgeSettings")}</Link>}</nav>}
      <PageHeader
        title={t(review ? "memoryReviewTitle" : "knowledge")}
        description={t("knowledgeDescription")}
        action={
          <div className="flex flex-wrap gap-2">
          {allowed("knowledge_manage") && <Button variant="outline" asChild><Link to={`/admin/${encodeURIComponent(tenantID)}${entityID ? `/entities/${encodeURIComponent(entityID)}` : ""}/knowledge/debug`}>{t("retrievalTitle")}</Link></Button>}
          {allowed("knowledge_write") && (
            <Button
              disabled={scope === "AGENT" && !agent}
              onClick={() => setCreating(!creating)}
            >
              {t("knowledgeWrite")}
            </Button>
          )}
          </div>
        }
      />
      {!review && allowed("knowledge_read") && <MetricsHistory tenant={tenantID} project={entityID}/>}
      {!review && allowed("knowledge_read") && <NativeSearch key={`search:${tenantID}:${entityID}`} tenant={tenantID} project={entityID}/>}
      {error && <Alert>{error}</Alert>}
      <div className="flex flex-wrap items-end gap-3">
        <label className="space-y-1 text-sm">
          {t("knowledgeScope")}
          <select
            className="block min-h-11 rounded border bg-background px-3"
            value={scope}
            onChange={(e) => {
              setScope(e.target.value as typeof scope)
              setCreating(false)
            }}
          >
            {["TEAM", ...(entityID ? ["PROJECT"] : []), "AGENT"].map(
              (value) => (
                <option key={value} value={value}>
                  {t(`knowledgeScope_${value}`)}
                </option>
              )
            )}
          </select>
        </label>
        <label className="space-y-1 text-sm">
          {t("knowledgeState")}
          <select
            className="block min-h-11 rounded border bg-background px-3"
            value={state}
            onChange={(e) => setState(e.target.value as typeof state)}
          >
            {["ACTIVE", "PENDING_REVIEW", "REJECTED", "REVOKED"].map(
              (value) => (
                <option key={value} value={value}>
                  {t(`knowledgeState_${value}`)}
                </option>
              )
            )}
          </select>
        </label>
        {scope === "AGENT" && (
          <form
            className="flex items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              setAgent(agentDraft)
            }}
          >
            <label className="space-y-1 text-sm">
              {t("knowledgeAgent")}
              <Input
                required
                inputMode="numeric"
                pattern="[1-9][0-9]*"
                value={agentDraft}
                onChange={(e) => setAgentDraft(e.target.value)}
              />
            </label>
            <Button variant="outline" type="submit">
              {t("knowledgeLoad")}
            </Button>
          </form>
        )}
        <Button
          variant="outline"
          disabled={loading || (scope === "AGENT" && !agent)}
          onClick={() => setRevision((n) => n + 1)}
        >
          {t("knowledgeRefresh")}
        </Button>
      </div>
      {creating && (
        <KnowledgeCreate
          key={JSON.stringify(writeScope)}
          tenantID={tenantID}
          scope={writeScope}
          onDone={refresh}
        />
      )}
      <div className="grid min-h-72 border md:grid-cols-[minmax(14rem,1fr)_2fr]">
        <div className="border-b md:border-r md:border-b-0" aria-busy={loading}>
          {loading && !items.length ? (
            <p className="p-4 text-sm">{t("knowledgeLoading")}</p>
          ) : !items.length ? (
            <p className="p-4 text-sm text-muted-foreground">
              {scope === "AGENT" && !agent
                ? t("knowledgeChooseAgent")
                : t("knowledgeEmpty")}
            </p>
          ) : (
            items.map((entry) => (
              <button
                key={entry.id}
                className={`block min-h-14 w-full border-b p-4 text-left hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring ${selected?.id === entry.id ? "bg-muted" : ""}`}
                onClick={() => setSelected(entry)}
                aria-pressed={selected?.id === entry.id}
              >
                <span className="block font-medium">{entry.title}</span>
                <span className="text-xs text-muted-foreground">
                  {t(`knowledgeKind_${entry.kind}`)} ·{" "}
                  {t(`knowledgeState_${entry.state}`)}
                </span>
              </button>
            ))
          )}
          {cursor && (
            <Button
              variant="ghost"
              disabled={loading}
              onClick={() => void more()}
            >
              {t("knowledgeMore")}
            </Button>
          )}
        </div>
        {selected ? (
          <article className="min-w-0 space-y-4 p-5">
            <h2 className="text-lg font-semibold">{selected.title}</h2>
            <p className="text-sm leading-7 break-words whitespace-pre-wrap">
              {selected.body}
            </p>
            <dl className="space-y-2 border-t pt-4 text-sm">
              <dt className="text-muted-foreground">
                {t("knowledgeProvenance")}
              </dt>
              <dd className="break-words">{selected.provenance}</dd>
              <dt className="text-muted-foreground">{t("knowledgeVersion")}</dt>
              <dd>{selected.version}</dd>
            </dl>
            {((selected.state === "PENDING_REVIEW" &&
              allowed("memory_approve")) ||
              (selected.state === "ACTIVE" &&
                allowed("knowledge_manage") &&
                allowed("memory_approve"))) && (
              <KnowledgeReviewForm
                key={`${selected.id}:${selected.version}`}
                tenantID={tenantID}
                entry={selected}
                subject={subject}
                onDone={refresh}
              />
            )}
          </article>
        ) : (
          <p className="p-5 text-sm text-muted-foreground">
            {t("knowledgeSelect")}
          </p>
        )}
      </div>
    </div>
  )
}

function KnowledgeCreate({
  tenantID,
  scope,
  onDone,
}: {
  tenantID: string
  scope: Pick<KnowledgeWrite, "scope" | "project_id" | "agent_principal_id">
  onDone: () => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute("post", "/tenants/{tenant_id}/knowledge", {
        path: { tenant_id: tenantID },
      }),
    [tenantID]
  )
  const metadata = useRequestContract(route)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const [error, setError] = useState("")
  return (
    <div className="space-y-3 border p-4">
      <h2 className="font-medium">{t("knowledgeWrite")}</h2>
      <p className="text-sm text-muted-foreground">
        {t("knowledgeRuleNotice")}
      </p>
      {error && <Alert>{error}</Alert>}
      {metadata.failed && (
        <Button variant="outline" onClick={metadata.retry}>
          {t("knowledgeRefresh")}
        </Button>
      )}
      <AutoForm
        contract={metadata.contract}
        fields={{
          scope: {
            name: t("knowledgeScope"),
            type: "text",
            value: scope.scope,
            show: false,
          },
          ...(scope.project_id
            ? {
                project_id: {
                  name: t("knowledgeScope_PROJECT"),
                  type: "text" as const,
                  value: scope.project_id,
                  show: false,
                },
              }
            : {}),
          ...(scope.agent_principal_id
            ? {
                agent_principal_id: {
                  name: t("knowledgeAgent"),
                  type: "text" as const,
                  value: scope.agent_principal_id,
                  show: false,
                },
              }
            : {}),
          kind: {
            name: t("knowledgeKind"),
            type: "select",
            value: "DOCUMENT",
            config: {
              options: ["DOCUMENT", "EXPERIENCE", "RULE"].map((value) => ({
                label: t(`knowledgeKind_${value}`),
                value,
              })),
            },
          },
          title: { name: t("knowledgeTitle"), type: "text", value: "" },
          body: {
            name: t("knowledgeBody"),
            type: "textarea",
            value: "",
            config: { rows: 6 },
          },
          provenance: {
            name: t("knowledgeProvenance"),
            type: "text",
            value: "",
          },
        }}
        onSubmit={async (values) => {
          const body: KnowledgeWrite = {
            ...scope,
            kind: String(values.kind) as KnowledgeWrite["kind"],
            title: String(values.title),
            body: String(values.body),
            provenance: String(values.provenance),
          }
          const identity = JSON.stringify(body)
          const key =
            attempt.current?.body === identity
              ? attempt.current.key
              : crypto.randomUUID()
          attempt.current = { body: identity, key }
          try {
            await iamApi.writeKnowledge(tenantID, body, key)
            onDone()
          } catch (e) {
            setError((e as Error).message)
            throw e
          }
        }}
      >
        <Button type="submit" disabled={!metadata.contract}>
          {t("knowledgeWrite")}
        </Button>
      </AutoForm>
    </div>
  )
}

export function KnowledgeReviewForm({
  tenantID,
  entry,
  subject,
  onDone,
}: {
  tenantID: string
  entry: KnowledgeEntry
  subject: string
  onDone: () => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      entry.id
        ? apiRoute(
            "post",
            "/tenants/{tenant_id}/knowledge/{entry_id}/reviews",
            { path: { tenant_id: tenantID, entry_id: entry.id } }
          )
        : null,
    [tenantID, entry.id]
  )
  const metadata = useRequestContract(route)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const [error, setError] = useState("")
  const [acknowledged, setAcknowledged] = useState(false)
  const [revocation, setRevocation] = useState<KnowledgeEntry | null>(null)
  const [readingAffected, setReadingAffected] = useState(false)
  if (!subject) return null
  if (entry.state === "PENDING_REVIEW" && entry.created_by === subject) {
    return <p role="status" className="border-t pt-4 text-sm text-muted-foreground">{t("knowledgeSelfReview")}</p>
  }
  if (revocation) return <section className="space-y-3 border-t pt-4" aria-label={t("knowledgeAffectedExecutions")}>
    <h3 className="font-medium">{t("knowledgeAffectedExecutions")}</h3>
    <p className="text-sm text-muted-foreground">{t("knowledgeRevocationHistory")}</p>
    {error && <Alert>{error}</Alert>}
    <ul>{(revocation.affected_execution_ids ?? []).map(id => <li key={id}>{id}</li>)}</ul>
    {!revocation.affected_execution_ids?.length && <p>{t("knowledgeAffectedEmpty")}</p>}
    {revocation.affected_executions_cursor && <Button variant="outline" disabled={readingAffected} onClick={async () => {
      if (!entry.id) return
      setReadingAffected(true)
      try {
        const page = await iamApi.deliveryResultRequest<components["schemas"]["AffectedExecutionPage"]>(apiRoute("get", "/tenants/{tenant_id}/knowledge/{entry_id}/affected-executions", {path:{tenant_id:tenantID, entry_id:entry.id}, query:{cursor:revocation.affected_executions_cursor, limit:50}}))
        setRevocation(current => current && ({...current, affected_execution_ids:[...new Set([...(current.affected_execution_ids ?? []), ...(page.items ?? [])])], affected_executions_cursor:page.next_cursor}))
        setError("")
      } catch (cause) { setError((cause as Error).message) } finally { setReadingAffected(false) }
    }}>{t("knowledgeMore")}</Button>}
    <Button onClick={onDone}>{t("knowledgeRefresh")}</Button>
  </section>
  return (
    <div className="space-y-3 border-t pt-4">
      <h3 className="font-medium">{t("knowledgeReview")}</h3>
      {!!entry.conflict_memory_ids?.length && <div className="space-y-2">
        <p role="status">{t("knowledgeConflictingRules")}: {entry.conflict_memory_ids.join(", ")}</p>
        <label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />{t("knowledgeConflictAcknowledgement")}</label>
      </div>}
      {error && <Alert>{error}</Alert>}
      <AutoForm
        contract={metadata.contract}
        fields={{
          version: {
            name: t("knowledgeVersion"),
            type: "number",
            value: entry.version,
            show: false,
          },
          action: {
            name: t("knowledgeAction"),
            type: "select",
            value: entry.state === "ACTIVE" ? "REVOKED" : "APPROVED",
            config: {
              options: (entry.state === "ACTIVE"
                ? ["REVOKED"]
                : ["APPROVED", "REJECTED"]
              ).map((value) => ({
                label: t(`knowledgeAction_${value}`),
                value,
              })),
            },
          },
          reason: { name: t("knowledgeReason"), type: "textarea", value: "" },
        }}
        onSubmit={async (values) => {
          if (!entry.id) return
          const body: KnowledgeReview = {
            version: entry.version,
            action: String(values.action) as KnowledgeReview["action"],
            reason: String(values.reason),
            conflicts_acknowledged: acknowledged,
          }
          if (body.action === "APPROVED" && entry.conflict_memory_ids?.length && !acknowledged) {
            const message = t("knowledgeConflictAcknowledgement")
            setError(message)
            throw new Error(message)
          }
          const identity = JSON.stringify(body)
          const key =
            attempt.current?.body === identity
              ? attempt.current.key
              : crypto.randomUUID()
          attempt.current = { body: identity, key }
          try {
            const result = await iamApi.reviewKnowledge(tenantID, entry.id, body, key)
            if (body.action === "REVOKED") setRevocation(result)
            else onDone()
          } catch (e) {
            setError((e as Error).message)
            throw e
          }
        }}
      >
        <Button type="submit" disabled={!metadata.contract}>
          {t("knowledgeReview")}
        </Button>
      </AutoForm>
    </div>
  )
}
