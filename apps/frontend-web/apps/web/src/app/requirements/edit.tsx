import { useEffect, useMemo, useRef, useState } from "react"
import { AutoForm, type FormConfig } from "@workspace/ui/components/auto-form"
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
import { iamApi, type RequirementDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"

export type DraftEdit =
  { kind: "metadata" | "new" } | { kind: "criterion"; index: number }

export function EditRequirement({
  entityID,
  detail,
  edit,
  onClose,
  onSaved,
}: {
  entityID: string
  detail: RequirementDetail
  edit: DraftEdit
  onClose: () => void
  onSaved: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const criterion =
    edit.kind === "criterion" ? detail.criteria?.[edit.index] : undefined
  const id = detail.id ?? ""
  const criterionID = criterion?.id ?? ""
  const route = useMemo(
    () =>
      edit.kind === "metadata"
        ? apiRoute(
            "patch",
            "/entities/{entity_id}/requirements/{requirement_id}",
            { path: { entity_id: entityID, requirement_id: id } }
          )
        : edit.kind === "new"
          ? apiRoute(
              "post",
              "/entities/{entity_id}/requirements/{requirement_id}/acceptance-criteria",
              { path: { entity_id: entityID, requirement_id: id } }
            )
          : apiRoute(
              "patch",
              "/entities/{entity_id}/requirements/{requirement_id}/acceptance-criteria/{criterion_id}",
              {
                path: {
                  entity_id: entityID,
                  requirement_id: id,
                  criterion_id: criterionID,
                },
              }
            ),
    [edit.kind, entityID, id, criterionID]
  )
  const metadata = useRequestContract(route)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef(false)
  const active = useRef(true)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  const fields: FormConfig =
    edit.kind === "metadata"
      ? {
          title: { name: t("workTitle"), type: "text", disabled: busy },
          description: {
            name: t("description"),
            type: "textarea",
            disabled: busy,
          },
          priority: {
            name: t("requirementPriority"),
            type: "select",
            disabled: busy,
            config: {
              options: [0, 1, 2, 3].map((value) => ({
                value: String(value),
                label: `P${value}`,
              })),
            },
          },
        }
      : {
          text: {
            name: t("workCriterionText"),
            type: "textarea",
            disabled: busy,
          },
          required: {
            name: t("requirementRequired"),
            type: "checkbox",
            disabled: busy,
          },
          position: {
            name: t("requirementOrder"),
            type: "number",
            disabled: busy,
          },
          ...(edit.kind === "criterion"
            ? {
                archived: {
                  name: t("requirementArchiveCriterion"),
                  type: "checkbox" as const,
                  disabled: busy,
                },
              }
            : {}),
        }
  const initialData =
    edit.kind === "metadata"
      ? {
          title: detail.title,
          description: detail.description,
          priority: String(detail.priority),
        }
      : {
          text: criterion?.text ?? "",
          required: criterion?.required ?? true,
          position:
            criterion?.position ?? Math.min(49, detail.criteria?.length ?? 0),
          archived: false,
        }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {edit.kind === "new" ? t("workAddClause") : t("requirementEdit")}
          </DialogTitle>
          <DialogDescription>{t("requirementDraftEditHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {metadata.failed && (
          <Alert>
            {t("projectContractUnavailable")}{" "}
            <Button variant="outline" size="sm" onClick={metadata.retry}>
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        <AutoForm
          id="requirement-edit-form"
          mode="edit"
          initialData={initialData}
          fields={fields}
          contract={metadata.contract}
          onSubmit={async (values) => {
            if (
              pending.current ||
              !metadata.contract ||
              !allowed("requirement_write") ||
              detail.status !== "DRAFT"
            )
              return
            const body =
              edit.kind === "metadata"
                ? {
                    title: String(values.title),
                    description: String(values.description ?? ""),
                    priority: Number(values.priority),
                  }
                : {
                    text: String(values.text),
                    required: Boolean(values.required),
                    position: Number(values.position),
                    ...(edit.kind === "criterion"
                      ? { archived: Boolean(values.archived) }
                      : {}),
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
              if (edit.kind === "metadata")
                await iamApi.updateRequirement(
                  entityID,
                  id,
                  detail.version,
                  body,
                  key
                )
              else if (edit.kind === "new")
                await iamApi.createRequirementCriterion(
                  entityID,
                  id,
                  detail.version,
                  {
                    text: String(values.text),
                    required: Boolean(values.required),
                    position: Number(values.position),
                  },
                  key
                )
              else
                await iamApi.updateRequirementCriterion(
                  entityID,
                  id,
                  criterionID,
                  detail.version,
                  {
                    text: String(values.text),
                    required: Boolean(values.required),
                    position: Number(values.position),
                    archived: Boolean(values.archived),
                  },
                  key
                )
              if (active.current) onSaved()
            } catch (cause) {
              if (active.current)
                setError(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
            } finally {
              pending.current = false
              if (active.current) setBusy(false)
            }
          }}
          onError={(cause) =>
            setError(
              cause instanceof Error ? cause.message : t("workUnavailable")
            )
          }
        />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form="requirement-edit-form"
            disabled={
              busy || !metadata.contract || !allowed("requirement_write")
            }
          >
            {busy ? t("saving") : t("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
