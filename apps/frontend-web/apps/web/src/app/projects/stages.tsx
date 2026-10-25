import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type ProjectDetail,
  type ProjectStageChange,
  type ProjectStageReadiness,
  type ProjectStageTransition,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"

export function ProjectStages({
  project,
  onChanged,
}: {
  project: ProjectDetail
  onChanged: () => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [targets, setTargets] = useState<ProjectStageReadiness[]>([])
  const [history, setHistory] = useState<ProjectStageTransition[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<ProjectStageReadiness | null>(null)
  const [busy, setBusy] = useState(false)
  const [cancelBody,setCancelBody]=useState<ProjectStageChange|null>(null)
  const pending = useRef<AbortController | null>(null)
  const changing = useRef(false)
  const mounted = useRef(true)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const route = useMemo(
    () =>
      apiRoute("post", "/entities/{entity_id}/project/stage-transitions", {
        path: { entity_id: project.project_id! },
      }),
    [project.project_id]
  )
  const metadata = useRequestContract(selected?.allowed ? route : null)
  const readOnly =
    !!project.archived_at || ["CLOSED", "CANCELLED"].includes(project.stage)
  const load = useCallback(async () => {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true)
    setError("")
    try {
      const [readiness, page] = await Promise.all([
        readOnly
          ? Promise.resolve([])
          : iamApi.projectStageReadiness(
              project.project_id!,
              controller.signal
            ),
        iamApi.projectStageHistory(
          project.project_id!,
          offset,
          10,
          controller.signal
        ),
      ])
      if (controller.signal.aborted) return
      setTargets(readiness)
      setHistory(page.items)
      setTotal(page.total)
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [project.project_id, offset, readOnly, t])
  useEffect(() => {
    mounted.current = true
    let active = true
    queueMicrotask(() => {
      if (active) void load()
    })
    return () => {
      active = false
      mounted.current = false
      pending.current?.abort()
    }
  }, [load, project.version])
  const submit=async(body:ProjectStageChange)=>{
 if(changing.current)return
                    const identity = JSON.stringify({version:project.version,body:{...body,reauth_proof:undefined}})
                    const key =
                      attempt.current?.identity === identity
                        ? attempt.current.key
                        : crypto.randomUUID()
                    attempt.current = { identity, key }
                    changing.current = true
                    setBusy(true)
                    setError("")
                    try {
                      await iamApi.changeProjectStage(
                        project.project_id!,
                        project.version,
                        body,
                        key
                      )
                      if (mounted.current) {
                        setSelected(null)
                        setCancelBody(null)
                        onChanged()
                        void load()
                      }
                    } catch (cause) {
                      if (mounted.current)
                        setError(
                          cause instanceof Error
                            ? cause.message
                            : t("workUnavailable")
                        )
                    } finally {
                      changing.current = false
                      if (mounted.current) setBusy(false)
                    }
  }
  const stageLabel = (value: string) =>
    t.has(`project_stage_${value}`) ? t(`project_stage_${value}`) : value
  return (
    <section className="grid gap-3 py-4" aria-label={t("stageTitle")}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-medium">{t("stageTitle")}</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={loading || busy}
          onClick={() => {
            void load()
            onChanged()
          }}
        >
          {t("refresh")}
        </Button>
      </div>
      {error && <Alert>{error}</Alert>}
      {loading && <p role="status">{t("loading")}</p>}
      <div className="flex flex-wrap gap-2">
        {targets.map((target) => (
          <Button
            key={target.target}
            variant={target.allowed ? "default" : "outline"}
            disabled={loading || busy}
            onClick={() => setSelected(target)}
          >
            {stageLabel(target.target)}
            {!target.allowed ? ` · ${t("stagePrerequisites")}` : ""}
          </Button>
        ))}
      </div>
      <AutoTable
        data={history}
        rowOffset={offset}
        loading={loading}
        pagination={false}
        rowKey="id"
        columns={[
          {
            field: "from_stage",
            name: t("stageFrom"),
            format: (_, row) => stageLabel(row.from_stage),
          },
          {
            field: "to_stage",
            name: t("stageTo"),
            format: (_, row) => stageLabel(row.to_stage),
          },
          { field: "reason", name: t("stageReason"), width: "300px" },
          { field: "actor_id", name: t("stageActor"), width: "180px" },
          {
            field: "authority_source",
            name: t("stageAuthority"),
            width: "220px",
          },
          {
            field: "occurred_at",
            name: t("createdAt"),
            width: "210px",
            format: (_, row) =>
              new Date(row.occurred_at).toLocaleString(locale),
          },
        ]}
        footer={
          <PaginationPages
            page={Math.floor(offset / 10) + 1}
            totalPages={Math.max(1, Math.ceil(total / 10))}
            onPageChange={(page) => setOffset((page - 1) * 10)}
          />
        }
      />
      {cancelBody && <ReauthenticationDialog purpose="project_cancel" target={`project:${project.project_id}`} onClose={()=>{if(!busy)setCancelBody(null)}} onVerified={proof=>submit({...cancelBody,reauth_proof:proof})}/>}
      {selected && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open && !busy) setSelected(null)
          }}
        >
          <DialogContent className="max-h-[90dvh] overflow-y-auto">
            <DialogHeader>
              <DialogTitle>
                {t("stageConfirm", { stage: stageLabel(selected.target) })}
              </DialogTitle>
              <DialogDescription>{t(selected.target==="CANCELLED"?"stageCancelConsequences":"stageCommandHint")}</DialogDescription>
            </DialogHeader>
            <ul className="grid gap-2 text-sm">
              {(selected.criteria ?? []).map((criterion) => (
                <li key={criterion.key}>
                  {criterion.passed ? "✓" : "✗"}{" "}
                  {t.has(`stageCriterion_${criterion.key}`)
                    ? t(`stageCriterion_${criterion.key}`)
                    : criterion.detail}
                </li>
              ))}
            </ul>
            {error && <Alert>{error}</Alert>}
            {metadata.failed && (
              <Alert>
                {t("workUnavailable")}{" "}
                <Button variant="outline" onClick={metadata.retry}>
                  {t("retryNow")}
                </Button>
              </Alert>
            )}
            {selected.allowed &&
              !readOnly &&
              allowed("project_stage_change") && (
                <AutoForm
                  key={`${selected.target}:${project.version}`}
                  id="project-stage-form"
                  mode="edit"
                  contract={metadata.contract ?? undefined}
                  initialData={{
                    to_stage: selected.target,
                    reason: "",
                    blocked_kind: "OTHER",
                  }}
                  fields={{
                    to_stage: {
                      name: t("stageTo"),
                      type: "select",
                      config: {
                        options: [
                          {
                            value: selected.target,
                            label: stageLabel(selected.target),
                          },
                        ],
                      },
                    },
                    reason: {
                      name: t("stageReason"),
                      type: "textarea",
                      required:
                        selected.target === "CANCELLED" || project.stage === "DELIVERED" || selected.target === "BLOCKED" ||
                        project.stage === "BLOCKED" ||
                        (selected.target === "DISCOVERY" &&
                          project.stage === "PLANNING") ||
                        (selected.target === "ACTIVE" &&
                          project.stage === "ACCEPTANCE_PENDING"),
                    },
                    confirm_code:{name:t("stageCancelCode"),type:"text",required:selected.target==="CANCELLED",show:selected.target==="CANCELLED"},
                    scope_change_id:{name:t("stageScopeChange"),type:"text",required:project.stage==="DELIVERED"&&selected.target==="ACTIVE",show:project.stage==="DELIVERED"&&selected.target==="ACTIVE"},
                    blocked_kind: {
                      name: t("stageBlockedKind"),
                      type: "select",
                      show: selected.target === "BLOCKED",
                      config: {
                        options: [
                          "CLIENT_CLARIFICATION",
                          "DEPENDENCY",
                          "EXTERNAL",
                          "OTHER",
                        ].map((value) => ({
                          value,
                          label: t(`stageBlocked_${value}`),
                        })),
                      },
                    },
                  }}
                  onSubmit={async (values) => {
                    if (changing.current || !metadata.contract) return
                    const body: ProjectStageChange = {
                      to_stage:
                        selected.target as ProjectStageChange["to_stage"],
                      reason: String(values.reason ?? ""),
                    }
                    if (selected.target === "BLOCKED")
                      body.blocked_kind = String(
                        values.blocked_kind
                      ) as ProjectStageChange["blocked_kind"]
                    if(selected.target === "CANCELLED"){
                      body.confirm_code=String(values.confirm_code??"")
                      setCancelBody(body)
                    }else{
                      if(project.stage === "DELIVERED" && selected.target === "ACTIVE") body.scope_change_id=String(values.scope_change_id??"")
                      await submit(body)
                    }
                  }}
                />
              )}
            <DialogFooter>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => setSelected(null)}
              >
                {t("cancel")}
              </Button>
              {selected.allowed &&
                !readOnly &&
                allowed("project_stage_change") && (
                  <Button
                    type="submit"
                    form="project-stage-form"
                    disabled={busy || !metadata.contract}
                  >
                    {busy ? t("saving") : t("stageApply")}
                  </Button>
                )}
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </section>
  )
}
