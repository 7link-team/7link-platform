import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import { iamApi, type ProjectCreateInput } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import {ProjectClientField, ProjectContactsField, ProjectMembersField, ProjectRepositoriesField} from "./create-fields"

export function ProjectCreateAction({ tenantID }: { tenantID: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [open, setOpen] = useState(false)
  if (!tenantID || !allowed("project_manage")) return null
  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus />
        {t("createProject")}
      </Button>
      {open && (
        <ProjectCreateDialog
          key={tenantID}
          tenantID={tenantID}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

function ProjectCreateDialog({
  tenantID,
  onClose,
}: {
  tenantID: string
  onClose: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const navigate = useNavigate()
  const route = useMemo(
    () =>
      apiRoute("post", "/tenants/{tenant_id}/projects", {
        path: { tenant_id: tenantID },
      }),
    [tenantID]
  )
  const metadata = useRequestContract(route)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const active = useRef(true)
  const pending = useRef(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  const submit = async (values: Record<string, unknown>) => {
    if (pending.current || !allowed("project_manage") || !metadata.contract)
      return
    const body: ProjectCreateInput = {
      ...(values as ProjectCreateInput),
      name: String(values.name),
      code: String(values.code),
      description: String(values.description ?? ""),
    }
    const identity = JSON.stringify(body)
    const key =
      attempt.current?.body === identity
        ? attempt.current.key
        : crypto.randomUUID()
    attempt.current = { body: identity, key }
    pending.current = true
    setBusy(true)
    setError("")
    try {
      const created = await iamApi.createProject(tenantID, body, key)
      if (!active.current) return
      const id = created.project.project_id
      if (!id || id !== created.entity.id)
        throw new Error(t("projectCreateFailed"))
      window.dispatchEvent(new Event("entity-catalog-change"))
      navigate(
        `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(id)}/project`
      )
      onClose()
    } finally {
      pending.current = false
      if (active.current) setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("createProject")}</DialogTitle>
          <DialogDescription>{t("createProjectDescription")}</DialogDescription>
        </DialogHeader>
        {metadata.failed && (
          <Alert>
            {t("projectContractUnavailable")}{" "}
            <Button variant="outline" onClick={metadata.retry}>
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        {error && <Alert>{error}</Alert>}
        <AutoForm
          id="project-create-form"
          contract={metadata.contract}
          readonly={!allowed("project_manage")}
          fields={{
            name: {
              name: t("name"),
              type: "text",
              value: "",
              disabled: busy,
              config: { autoFocus: true },
            },
            code: {
              name: t("projectCode"),
              type: "text",
              value: "",
              disabled: busy,
              config: { placeholder: t("projectCodeHint") },
            },
            description: {
              name: t("description"),
              type: "textarea",
              value: "",
              disabled: busy,
            },
            target_date: {name: t("clientTargetDate"), type: "text", disabled: busy, config: {type: "date"}},
            client: {name: t("projectClientChoice"), type: "custom", component: ProjectClientField, disabled: busy},
            contacts: {name: t("projectContacts"), type: "custom", component: ProjectContactsField, value: [], disabled: busy},
            repositories: {name: t("projectRepositories"), type: "custom", component: ProjectRepositoriesField, value: [], disabled: busy},
            members: {name: t("projectMembers"), type: "custom", component: ProjectMembersField, value: [], disabled: busy},
            budget_cap_micros: {name: t("projectCreateBudget"), type: "number", disabled: busy, config: {min: 1, max: Number.MAX_SAFE_INTEGER, step: 1}},
            budget_notify_pct: {name: t("govSetting_default_budget_notify_pct"), type: "number", value: 80, disabled: busy, config: {min: 50, max: 95, step: 1}},
          }}
          onSubmit={submit}
          onError={(cause) =>
            setError(
              cause instanceof Error ? cause.message : t("projectCreateFailed")
            )
          }
        />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form="project-create-form"
            disabled={busy || !metadata.contract || !allowed("project_manage")}
          >
            {busy ? t("saving") : t("createProject")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
