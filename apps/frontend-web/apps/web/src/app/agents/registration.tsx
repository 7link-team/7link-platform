import { useEffect, useId, useMemo, useRef, useState } from "react"
import { Link } from "react-router"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { useRequestContract } from "../../lib/use-request-contract"
import { knowledgeSkills, type Skill } from "../knowledge/skills-api"
import { buildAgentCreation } from "./registration-api"

type Creation = components["schemas"]["AgentCreation"]
type AvatarStyle = "PIXEL" | "ROBOT" | "MONSTER" | "GEOMETRIC"

function RegistrationChannels({
  value,
  onChange,
  disabled,
  labelId,
  describedBy,
  error,
}: FieldRendererProps) {
  return (
    <Textarea
      value={Array.isArray(value) ? value.join("\n") : ""}
      disabled={disabled}
      aria-labelledby={labelId}
      aria-describedby={describedBy}
      aria-invalid={Boolean(error)}
      onChange={(event) => onChange(event.target.value.split("\n"))}
    />
  )
}

function RegistrationModel({
  value,
  onChange,
  required,
  disabled,
  config,
  labelId,
  describedBy,
  error,
}: FieldRendererProps) {
  const list = useId()
  const models = Array.isArray(config.options) ? config.options : []
  return (
    <>
      <Input
        value={String(value ?? "")}
        onChange={(event) => onChange(event.target.value)}
        list={list}
        required={required}
        disabled={disabled}
        aria-labelledby={labelId}
        aria-describedby={describedBy}
        aria-invalid={Boolean(error)}
      />
      <datalist id={list}>
        {models.map((model) => (
          <option key={model.value} value={model.value} />
        ))}
      </datalist>
    </>
  )
}

function RegistrationAvatarStyle({
  value,
  onChange,
  config,
  disabled,
  labelId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  return (
    <select
      className="min-h-11 rounded-md border bg-background px-3"
      value={String(value ?? "PIXEL")}
      disabled={disabled}
      aria-labelledby={labelId}
      onChange={(event) => {
        onChange(event.target.value)
        const update = config.onStyle as (value: AvatarStyle) => void
        update(event.target.value as AvatarStyle)
      }}
    >
      {["PIXEL", "ROBOT", "MONSTER", "GEOMETRIC"].map((style) => (
        <option key={style} value={style}>
          {t(`avatarStyle_${style.toLowerCase()}`)}
        </option>
      ))}
    </select>
  )
}

function RegistrationAvatar({
  value,
  onChange,
  config,
  disabled,
  labelId,
  describedBy,
  error: fieldError,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const [batch, setBatch] = useState(0)
  const [rows, setRows] = useState<components["schemas"]["AvatarCandidate"][]>(
    []
  )
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const tenant = String(config.tenant)
  const style = String(config.style) as AvatarStyle
  useEffect(() => {
    const controller = new AbortController()
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) {
        setLoading(true)
        setError("")
      }
    })
    void iamApi
      .agentCreationAvatars(tenant, style, batch, controller.signal)
      .then(
        (rows) => {
          if (!controller.signal.aborted) setRows(rows)
        },
        (cause: Error) => {
          if (!controller.signal.aborted) setError(cause.message)
        }
      )
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [tenant, style, batch])
  return (
    <div className="grid gap-2" aria-labelledby={labelId} aria-busy={loading}>
      {error && <Alert>{error}</Alert>}
      <div className="grid grid-cols-3 gap-2">
        {rows.map((row) => (
          <button
            key={row.seed}
            type="button"
            disabled={disabled || loading}
            aria-label={`${t("agentAvatarCandidate")} ${row.seed}`}
            aria-pressed={Number(value) === row.seed}
            className="min-h-11 rounded-md border p-2 aria-pressed:ring-2 aria-pressed:ring-ring"
            onClick={() => onChange(row.seed)}
          >
            <img
              src={row.url}
              alt=""
              width={64}
              height={64}
              className="mx-auto size-16"
            />
          </button>
        ))}
      </div>
      <Input
        type="number"
        value={Number(value ?? 0)}
        min={0}
        max={9007199254740991}
        aria-labelledby={labelId}
        aria-describedby={describedBy}
        aria-invalid={Boolean(fieldError)}
        disabled={disabled}
        onChange={(event) => onChange(Number(event.target.value))}
      />
      <Button
        type="button"
        variant="outline"
        disabled={disabled || loading || batch >= 1000000}
        onClick={() => setBatch((value) => value + 1)}
      >
        {loading ? t("loading") : t("agentAvatarRefresh")}
      </Button>
    </div>
  )
}

export function AgentRegistration({
  tenant,
  onClose,
  onCreated,
}: {
  tenant: string
  onClose: () => void
  onCreated: (principal: string) => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute("post", "/tenants/{tenant_id}/agent-registrations", {
        path: { tenant_id: tenant },
      }),
    [tenant]
  )
  const metadata = useRequestContract(route)
  const [skills, setSkills] = useState<Skill[]>([])
  const [machines, setMachines] = useState<
    components["schemas"]["MachineView"][]
  >([])
  const [machine, setMachine] = useState("")
  const [runtime, setRuntime] = useState("")
  const [avatarStyle, setAvatarStyle] = useState<AvatarStyle>("PIXEL")
  const [capabilities, setCapabilities] = useState<
    components["schemas"]["MachineCapabilities"] | null
  >(null)
  const [loading, setLoading] = useState(true)
  const [inspecting, setInspecting] = useState(false)
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [review, setReview] = useState<Creation | null>(null)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    const load = async () => {
      setLoading(true)
      setError("")
      try {
        const rows: components["schemas"]["MachineView"][] = []
        let cursor: string | undefined
        do {
          const page = await iamApi.machines(tenant, cursor, controller.signal)
          rows.push(...(page.items ?? []))
          cursor = page.next_cursor
        } while (cursor && !controller.signal.aborted)
        if (controller.signal.aborted) return
        setMachines(
          rows.filter(
            (row) => row.id && row.state === "ONLINE" && !row.drain_requested
          )
        )
        const library: Skill[] = []
        let offset = 0
        do {
          const page = await knowledgeSkills.list(
            tenant,
            offset,
            "",
            "ACTIVE",
            controller.signal
          )
          library.push(...(page.items ?? []))
          if (!page.has_more) break
          offset = page.next_offset
        } while (!controller.signal.aborted)
        if (!controller.signal.aborted)
          setSkills(
            library.filter((skill) => skill.id && skill.current_version_id)
          )
      } catch (cause) {
        if (!controller.signal.aborted) setError((cause as Error).message)
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void load()
    return () => controller.abort()
  }, [tenant, revision])
  useEffect(() => {
    if (!machine) return
    const controller = new AbortController()
    void iamApi
      .machineCapabilities(machine, controller.signal)
      .then(
        (value) => {
          if (!controller.signal.aborted) setCapabilities(value)
        },
        (cause: Error) => {
          if (!controller.signal.aborted) setError(cause.message)
        }
      )
      .finally(() => {
        if (!controller.signal.aborted) setInspecting(false)
      })
    return () => controller.abort()
  }, [machine, revision])
  const runtimes =
    capabilities?.runtimes?.filter((row) =>
      ["CODEX", "CLAUDE_CODE"].includes(row.kind)
    ) ?? []
  const models = (
    runtimes.find((row) => row.kind === runtime)?.models ?? []
  ).filter((value): value is string => Boolean(value))
  const available =
    machines.some((row) => row.id === machine) &&
    runtimes.some((row) => row.kind === runtime)
  const submit = async () => {
    if (!review) return
    const identity = JSON.stringify(review)
    const key =
      attempt.current?.identity === identity
        ? attempt.current.key
        : crypto.randomUUID()
    attempt.current = { identity, key }
    setBusy(true)
    setError("")
    try {
      const created = await iamApi.createAgent(tenant, review, key)
      attempt.current = null
      if (created.principal_id) onCreated(created.principal_id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="max-h-[85svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("agentRegister")}</DialogTitle>
          <DialogDescription>{t("agentCreationHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {metadata.failed && <Alert>{t("agentRegisterUnavailable")}</Alert>}
        <div hidden={Boolean(review)}>
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <Link
              className="underline"
              to={`/admin/${encodeURIComponent(tenant)}/machines`}
            >
              {t("machines")}
            </Link>
            <Button
              type="button"
              variant="outline"
              disabled={loading || busy}
              onClick={() => {
                setInspecting(Boolean(machine))
                setCapabilities(null)
                setRuntime("")
                setRevision((value) => value + 1)
                metadata.retry()
              }}
            >
              {t("refresh")}
            </Button>
          </div>
          {(loading || inspecting) && <p role="status">{t("loading")}</p>}
          {!loading && (
            <>
              <label className="grid gap-2 text-sm">
                {t("fleetMachineID")}
                <select
                  className="min-h-11 rounded-md border bg-background px-3"
                  disabled={busy}
                  value={machine}
                  onChange={(event) => {
                    setMachine(event.target.value)
                    setInspecting(Boolean(event.target.value))
                    setCapabilities(null)
                    setRuntime("")
                  }}
                >
                  <option value="">{t("agentChooseMachine")}</option>
                  {machines.map((row) => (
                    <option key={row.id} value={row.id!}>
                      {row.name} · {row.id}
                    </option>
                  ))}
                </select>
              </label>
              {machine && !inspecting && (
                <label className="grid gap-2 text-sm">
                  {t("fleetRuntime")}
                  <select
                    className="min-h-11 rounded-md border bg-background px-3"
                    disabled={busy}
                    value={runtime}
                    onChange={(event) => setRuntime(event.target.value)}
                  >
                    <option value="">{t("agentChooseRuntime")}</option>
                    {runtimes.map((row) => (
                      <option key={row.kind} value={row.kind}>
                        {row.kind}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {machine && !inspecting && !runtimes.length && (
                <p role="status" className="text-sm text-muted-foreground">
                  {t("agentNoRuntimeModels")}
                </p>
              )}
            </>
          )}
          {available && !inspecting && (
            <>
              <p className="text-sm text-muted-foreground">
                {t("agentModelHint")}
              </p>
              <AutoForm
                key={`${machine}:${runtime}:${revision}`}
                contract={metadata.contract}
                fields={{
                  machine_id: {
                    name: t("fleetMachineID"),
                    type: "text",
                    value: machine,
                    disabled: true,
                  },
                  runtime: {
                    name: t("fleetRuntime"),
                    type: "text",
                    value: runtime,
                    disabled: true,
                  },
                  name: { name: t("agentName"), type: "text", disabled: busy },
                  handle: {
                    name: t("agentHandle"),
                    type: "text",
                    disabled: busy,
                  },
                  persona: {
                    name: t("agentPersona"),
                    type: "textarea",
                    value: "",
                    disabled: busy,
                  },
                  model: {
                    name: t("fleetModel"),
                    type: "custom",
                    component: RegistrationModel,
                    disabled: busy,
                    config: {
                      options: models.map((value) => ({ value, label: value })),
                    },
                  },
                  reasoning_effort: {
                    name: t("agentCreationReasoningEffort"),
                    type: "select",
                    disabled: busy,
                    config: {
                      options: ["low", "medium", "high", "xhigh"].map(
                        (value) => ({ value, label: value })
                      ),
                    },
                  },
                  roles: {
                    name: t("agentRoles"),
                    type: "checkbox-group",
                    value: ["EXECUTOR"],
                    disabled: busy,
                    config: {
                      options: [
                        "EXECUTOR",
                        "REVIEWER",
                        "COORDINATOR",
                        "LEAD",
                        "TESTER",
                        "ACCEPTOR",
                        "ASSISTANT",
                      ].map((value) => ({
                        value,
                        label: t(`agentRegistrationRole_${value}`),
                      })),
                    },
                  },
                  authorization_mode: {
                    name: t("executionAuthorization"),
                    type: "select",
                    value: "READ_ONLY",
                    disabled: busy,
                    config: {
                      options: [
                        {
                          value: "READ_ONLY",
                          label: t("agentAuthorizationReadOnly"),
                        },
                        {
                          value: "WORKSPACE_WRITE",
                          label: t("agentAuthorizationWrite"),
                        },
                      ],
                    },
                  },
                  avatar_style: {
                    name: t("avatarStyle"),
                    type: "custom",
                    component: RegistrationAvatarStyle,
                    value: "PIXEL",
                    disabled: busy,
                    config: { onStyle: setAvatarStyle },
                  },
                  avatar_seed: {
                    name: t("agentCreationAvatarSeed"),
                    type: "custom",
                    component: RegistrationAvatar,
                    value: 0,
                    disabled: busy,
                    config: { tenant, style: avatarStyle },
                  },
                  skill_ids: {
                    name: t("knowledgeSkills"),
                    type: "checkbox-group",
                    value: [],
                    disabled: busy,
                    config: {
                      options: skills.map((skill) => ({
                        value: skill.id!,
                        label: skill.title,
                      })),
                    },
                  },
                  memory_policy: {
                    name: t("agentMemoryPolicy"),
                    type: "textarea",
                    value: '{"profile":"STANDARD"}',
                    disabled: busy,
                  },
                  default_channels: {
                    name: t("agentDefaultChannels"),
                    type: "custom",
                    component: RegistrationChannels,
                    value: [],
                    disabled: busy,
                  },
                  system_prompt: {
                    name: t("agentSystemPrompt"),
                    type: "textarea",
                    disabled: busy,
                  },
                }}
                onSubmit={async (values) => {
                  try {
                    setReview(buildAgentCreation(values, machine, runtime))
                    setError("")
                  } catch (cause) {
                    setError((cause as Error).message)
                    throw cause
                  }
                }}
              >
                <Button type="submit" disabled={busy || !metadata.contract}>
                  {t("agentCreationReview")}
                </Button>
              </AutoForm>
            </>
          )}
        </div>
        {review && (
          <section className="grid gap-4" aria-label={t("agentCreationReview")}>
            <h3 className="font-semibold">{t("agentCreationReview")}</h3>
            <p className="text-sm text-muted-foreground">
              {t("agentCreationDeliveryHint")}
            </p>
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt>{t("agentName")}</dt>
              <dd>
                {review.name} · {review.handle}
              </dd>
              <dt>{t("fleetRuntime")}</dt>
              <dd>
                {review.runtime} · {review.model}
              </dd>
              <dt>{t("agentRoles")}</dt>
              <dd>{(review.roles ?? []).join(", ")}</dd>
              <dt>{t("knowledgeSkills")}</dt>
              <dd>{review.skill_ids?.length ?? 0}</dd>
              <dt>{t("agentMemoryPolicy")}</dt>
              <dd className="break-words">{review.memory_policy}</dd>
            </dl>
            <div className="flex gap-3">
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setReview(null)}
              >
                {t("back")}
              </Button>
              <Button
                type="button"
                disabled={busy}
                onClick={() => void submit()}
              >
                {busy ? t("loading") : t("agentRegister")}
              </Button>
            </div>
          </section>
        )}
      </DialogContent>
    </Dialog>
  )
}
