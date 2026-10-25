import { useEffect, useMemo, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import {
  rebindFailureDetails,
  rebindObservations,
} from "./machine-binding-capabilities"

type Binding = {
  reasoning_effort?: string
  capability_stale?: boolean
  agent_principal_id: string
  machine_id: string
  runtime_kind: string
  model_id: string
  state: string
  credential_delivery: string
  pending_cleanup_machine_id?: string
  config_revision: number
  version: number
}
type Command = {
  id: string
  machine_id: string
  kind: string
  state: string
  reason_code?: string
  expires_at: string
}

export function AgentMachineBinding({
  tenant,
  agent,
}: {
  tenant: string
  agent: string
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const canManage = allowed("agent_manage")
  const route = useMemo(
    () =>
      apiRoute(
        "put",
        "/tenants/{tenant_id}/agents/{agent_principal_id}/machine-binding",
        { path: { tenant_id: tenant, agent_principal_id: agent } }
      ),
    [tenant, agent]
  )
  const metadata = useRequestContract(route)
  const [binding, setBinding] = useState<Binding | null>(null)
  const [commands, setCommands] = useState<Command[]>([])
  const [machines, setMachines] = useState<
    components["schemas"]["MachineView"][]
  >([])
  const [capabilities, setCapabilities] = useState<
    components["schemas"]["MachineCapabilities"] | null
  >(null)
  const [machine, setMachine] = useState("")
  const [error, setError] = useState("")
  const [unknownMCP, setUnknownMCP] = useState<string[]>([])
  const [notice, setNotice] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    const load = async () => {
      setLoading(true)
      try {
        const current = await iamApi.deliveryResultRequest<Binding>(
          apiRoute(
            "get",
            "/tenants/{tenant_id}/agents/{agent_principal_id}/machine-binding",
            { path: { tenant_id: tenant, agent_principal_id: agent } }
          ),
          { signal: controller.signal }
        )
        const receipts = await iamApi.deliveryResultRequest<Command[]>(
          apiRoute(
            "get",
            "/tenants/{tenant_id}/agents/{agent_principal_id}/machine-commands",
            { path: { tenant_id: tenant, agent_principal_id: agent } }
          ),
          { signal: controller.signal }
        )
        const rows: components["schemas"]["MachineView"][] = []
        if (canManage) {
          let cursor: string | undefined
          do {
            const page = await iamApi.machines(
              tenant,
              cursor,
              controller.signal
            )
            rows.push(...(page.items ?? []))
            cursor = page.next_cursor
          } while (cursor && !controller.signal.aborted)
        }
        if (!controller.signal.aborted) {
          setBinding(current)
          setCommands(receipts)
          setMachines(
            rows.filter(
              (row) => row.id !== current.machine_id && row.state !== "REVOKED"
            )
          )
          setError("")
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError((cause as Error).message)
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void load()
    return () => controller.abort()
  }, [tenant, agent, revision, canManage])
  useEffect(() => {
    if (!machine) return
    const controller = new AbortController()
    void iamApi.machineCapabilities(machine, controller.signal).then(
      (value) => {
        if (!controller.signal.aborted) setCapabilities(value)
      },
      (cause) => {
        if (!controller.signal.aborted) setError((cause as Error).message)
      }
    )
    return () => controller.abort()
  }, [machine, revision])
  const runtimes =
    capabilities?.runtimes?.filter((row) =>
      ["CODEX", "CLAUDE_CODE"].includes(row.kind)
    ) ?? []
  return (
    <section
      className="space-y-4 border-t pt-5"
      aria-label={t("agentMachineBinding")}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-semibold">{t("agentMachineBinding")}</h3>
        <Button
          type="button"
          variant="outline"
          disabled={busy || loading}
          onClick={() => {
            setRevision((value) => value + 1)
            metadata.retry()
          }}
        >
          {t("refresh")}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">{t("agentRebindHelp")}</p>
      {error && <Alert>{error}</Alert>}
      {unknownMCP.length > 0 && (
        <Alert>
          {t("agentRebindUnknownMCP")}: {unknownMCP.join(", ")}
        </Alert>
      )}
      {notice && <p role="status">{notice}</p>}
      {loading && <p role="status">{t("loading")}</p>}
      {binding && (
        <>
          {binding.capability_stale && (
            <Alert>{t("agentRebindCapabilityStale")}</Alert>
          )}
          <dl className="grid gap-4 text-sm sm:grid-cols-3">
            {[
              ["fleetMachineID", binding.machine_id],
              [
                "agentCredentialDelivery",
                t(`agentBindingStatus_${binding.credential_delivery}`),
              ],
              [
                "agentPendingCleanup",
                binding.pending_cleanup_machine_id ??
                  t("agentNoPendingCleanup"),
              ],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-muted-foreground">{t(label!)}</dt>
                <dd className="mt-1 break-all">{value}</dd>
              </div>
            ))}
          </dl>
          {allowed("agent_manage") && (
            <>
              <label className="grid gap-2 text-sm">
                {t("agentRebindTarget")}
                <select
                  className="h-11 rounded-md border bg-background px-3"
                  value={machine}
                  disabled={busy || loading}
                  onChange={(event) => {
                    setMachine(event.target.value)
                    setCapabilities(null)
                    setUnknownMCP([])
                    setError("")
                  }}
                >
                  <option value="">{t("agentRebindSelectMachine")}</option>
                  {machines.map((row) => (
                    <option key={row.id} value={row.id ?? ""}>
                      {row.name} · {row.state}
                    </option>
                  ))}
                </select>
              </label>
              {machine && capabilities && (
                <div className="space-y-3">
                  <p className="text-sm text-muted-foreground">
                    {t("agentRebindCapabilityEvidence")}
                  </p>
                  <ul className="space-y-2 text-sm">
                    {rebindObservations(capabilities).map((fact) => (
                      <li key={`${fact.kind}:${fact.runtime}`}>
                        <span className="font-medium">
                          {fact.runtime} · {fact.kind} · {fact.state} ·{" "}
                          {fact.source}
                        </span>
                        {fact.checkedAt && (
                          <time className="ml-2" dateTime={fact.checkedAt}>
                            {new Date(fact.checkedAt).toLocaleString()}
                          </time>
                        )}
                        {fact.kind === "CAPABILITIES" && (
                          <p>MCP: {fact.mcp.join(", ") || "—"}</p>
                        )}
                        {fact.reasoning.map((model) => (
                          <p key={model.model}>
                            {model.model}: {model.efforts.join(", ") || "—"}
                          </p>
                        ))}
                      </li>
                    ))}
                  </ul>
                  <AutoForm
                    key={`${machine}:${binding.version}`}
                    contract={metadata.contract}
                    fields={{
                      machine_id: {
                        name: t("fleetMachineID"),
                        value: machine,
                        type: "text",
                        show: false,
                      },
                      runtime_kind: {
                        name: t("agentRuntimeKind"),
                        type: "select",
                        value:
                          runtimes.find(
                            (row) => row.kind === binding.runtime_kind
                          )?.kind ??
                          runtimes[0]?.kind ??
                          "",
                        config: {
                          options: runtimes.map((row) => ({
                            value: row.kind,
                            label: row.kind,
                          })),
                        },
                      },
                      model_id: {
                        name: t("agentBrowseModel"),
                        type: "text",
                        value: binding.model_id,
                      },
                      reasoning_effort: {
                        name: t("agentRebindReasoningEffort"),
                        type: "text",
                        value: binding.reasoning_effort ?? "",
                      },
                    }}
                    onSubmit={async (values) => {
                      setBusy(true)
                      setError("")
                      setUnknownMCP([])
                      setNotice("")
                      const body = {
                        machine_id: machine,
                        runtime_kind: String(values.runtime_kind),
                        model_id: String(values.model_id),
                        reasoning_effort: String(values.reasoning_effort ?? ""),
                      }
                      const identity = JSON.stringify({
                        version: binding.version,
                        body,
                      })
                      const key =
                        attempt.current?.identity === identity
                          ? attempt.current.key
                          : crypto.randomUUID()
                      attempt.current = { identity, key }
                      try {
                        const result =
                          await iamApi.deliveryResultRequest<Binding>(route, {
                            headers: {
                              "Idempotency-Key": key,
                              "If-Match": `"${binding.version}"`,
                            },
                            body: JSON.stringify(body),
                          })
                        setBinding(result)
                        setMachine("")
                        setCapabilities(null)
                        setNotice(t("agentRebindQueued"))
                        attempt.current = null
                        setRevision((value) => value + 1)
                      } catch (cause) {
                        setError((cause as Error).message)
                        setUnknownMCP(rebindFailureDetails(cause))
                        throw cause
                      } finally {
                        setBusy(false)
                      }
                    }}
                  >
                    <Button
                      type="submit"
                      disabled={
                        busy ||
                        !metadata.contract ||
                        !runtimes.length ||
                        !["DRAFT", "STOPPED", "CRASHED"].includes(binding.state)
                      }
                    >
                      {t("agentRebindSubmit")}
                    </Button>
                  </AutoForm>
                </div>
              )}
            </>
          )}
          <AutoTable
            data={commands}
            rowKey="id"
            columns={[
              { field: "machine_id", name: t("fleetMachineID") },
              {
                field: "kind",
                name: t("agentCommandKind"),
                format: (value) => t(`agentCommandKind_${value}`),
              },
              {
                field: "state",
                name: t("agentCommandState"),
                format: (value) => t(`agentBindingStatus_${value}`),
              },
              { field: "reason_code", name: t("agentCommandReason") },
            ]}
          />
        </>
      )}
    </section>
  )
}
