"use client"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"

import {
  AutoForm,
  type UseAutoFormReturn,
} from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useCallback, useEffect, useRef, useState } from "react"

import { Alert } from "../../components/alert"
import type {
  IamApi,
  Principal,
  PrincipalLifecycleBlocker,
  PrincipalLifecycleStatus,
} from "../../lib/iam-api"

// 每条阻塞原因都对应运维要去做的一件具体的事，所以写成祈使句而不是状态描述。
const BLOCKER_TEXT: Record<PrincipalLifecycleBlocker, string> = {
  already_anonymized: "erasureTombstone",
  legal_hold: "erasureLegalHold",
  no_retention_recorded: "erasureNoRetention",
  retention_not_expired: "erasureRetentionActive",
  not_suspended: "erasureActivePrincipal",
}

function toDateInput(value: string | null): string {
  if (!value) return ""
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ""
  return parsed.toISOString().slice(0, 10)
}

export function PrincipalLifecycleDialog({
  principal,
  iamApi,
  onClose,
}: {
  principal: Pick<Principal, "id"> | null
  iamApi: IamApi
  onClose: () => void
}) {
  const t = useTranslations("iam")

  const retentionForm = useRef<UseAutoFormReturn>(null)
  const loadGeneration = useRef(0)
  const [status, setStatus] = useState<PrincipalLifecycleStatus | null>(null)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [busy, setBusy] = useState(false)
  const [retainUntil, setRetainUntil] = useState("")
  const [legalHold, setLegalHold] = useState(false)
  const [reference, setReference] = useState("")
  const [confirmation, setConfirmation] = useState("")

  const load = useCallback(
    async (id: string) => {
      const generation = loadGeneration.current
      const next = await iamApi.principalLifecycle(id)
      if (generation !== loadGeneration.current) return
      setError("")
      setStatus(next)
      setRetainUntil(toDateInput(next.lifecycle.retain_until))
      setLegalHold(next.lifecycle.legal_hold)
      setReference(next.lifecycle.reference)
      retentionForm.current?.setValues({
        retain_until: toDateInput(next.lifecycle.retain_until),
        reference: next.lifecycle.reference,
        legal_hold: next.lifecycle.legal_hold,
      })
    },
    [iamApi]
  )

  useEffect(() => {
    const generation = ++loadGeneration.current
    if (principal)
      void load(principal.id).catch((cause: Error) => {
        if (generation === loadGeneration.current) setError(cause.message)
      })
    return () => {
      loadGeneration.current = generation + 1
    }
  }, [principal, load])

  const saveRetention = async () => {
    if (!principal || !status) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      // 版本随每次读取带回：两个管理员从同一屏做决定时，后一个会被告知策略已变，
      // 而不是悄悄覆盖前一个。
      await iamApi.setPrincipalRetention(principal.id, {
        version: status.lifecycle.version,
        retain_until: new Date(`${retainUntil}T00:00:00Z`).toISOString(),
        legal_hold: legalHold,
        reference,
      })
      setNotice(t("retentionUpdated"))
      await load(principal.id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const anonymize = async () => {
    if (!principal || !status) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await iamApi.anonymizePrincipal(principal.id, {
        version: status.lifecycle.version,
        confirmation,
      })
      setNotice(t("principalErased"))
      setConfirmation("")
      await load(principal.id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const blocked = (status?.blockers.length ?? 0) > 0
  const namedCorrectly =
    status !== null && confirmation.trim() === status.login_name

  return (
    <Dialog
      open={Boolean(principal)}
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="grid max-h-[85vh] gap-5 overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("retentionErasure")}</DialogTitle>
          <DialogDescription>{t("erasureDescription")}</DialogDescription>
        </DialogHeader>

        {error && <Alert>{error}</Alert>}
        {notice && (
          <p className="rounded-md border px-3 py-2 text-sm" role="status">
            {notice}
          </p>
        )}

        {status && (
          <>
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt className="text-muted-foreground">{t("loginIdentifier")}</dt>
              <dd>{status.login_name}</dd>
              <dt className="text-muted-foreground">{t("status")}</dt>
              <dd>
                {status.anonymized
                  ? t("erasedTombstone")
                  : status.suspended
                    ? t("disabled")
                    : t("currentlyActive")}
              </dd>
              <dt className="text-muted-foreground">{t("policyVersion")}</dt>
              <dd>{status.lifecycle.version}</dd>
            </dl>

            {blocked && (
              <ul className="grid gap-1 text-sm">
                {status.blockers.map((blocker) => (
                  <li key={blocker}>· {t(BLOCKER_TEXT[blocker])}</li>
                ))}
              </ul>
            )}

            {!status.anonymized && (
              <>
                <AutoForm
                  formRef={retentionForm}
                  mode="edit"
                  key={`${principal?.id}:${status.lifecycle.version}`}
                  fields={{
                    retain_until: {
                      name: t("retainUntil"),
                      type: "date",
                      value: retainUntil,
                      disabled: busy,
                      config: { id: "retain-until" },
                    },
                    reference: {
                      name: t("caseReference"),
                      type: "text",
                      value: reference,
                      disabled: busy,
                      config: { id: "retention-reference", maxLength: 200 },
                    },
                    legal_hold: {
                      name: t("legalHold"),
                      type: "checkbox",
                      value: legalHold,
                      disabled: busy,
                    },
                  }}
                  onChange={(values) => {
                    setRetainUntil(String(values.retain_until))
                    setReference(String(values.reference))
                    setLegalHold(values.legal_hold === true)
                  }}
                  onSubmit={async () => {
                    if (!busy && retainUntil && reference.trim())
                      await saveRetention()
                  }}
                >
                  <Button
                    className="md:col-span-2"
                    variant="outline"
                    type="submit"
                    disabled={busy || !retainUntil || !reference.trim()}
                  >
                    {busy ? t("processing") : t("saveRetention")}
                  </Button>
                </AutoForm>
                <AutoForm
                  id="anonymize-principal-form"
                  className="border-t pt-4"
                  fields={{
                    confirmation: {
                      name: (
                        <>
                          {t("enterLoginName")}
                          <b>{status.login_name}</b> {t("confirmErasureSuffix")}
                        </>
                      ),
                      type: "text",
                      value: confirmation,
                      disabled: busy || blocked,
                      col: 2,
                      config: {
                        id: "anonymize-confirmation",
                        autoComplete: "off",
                      },
                    },
                  }}
                  onChange={(values) =>
                    setConfirmation(String(values.confirmation))
                  }
                  onSubmit={async () => {
                    if (!busy && !blocked && namedCorrectly) await anonymize()
                  }}
                />
              </>
            )}
          </>
        )}

        <DialogFooter className="gap-2">
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("close")}
          </Button>
          {status && !status.anonymized && (
            <Button
              variant="destructive"
              disabled={busy || blocked || !namedCorrectly}
              type="submit"
              form="anonymize-principal-form"
            >
              {busy ? t("erasing") : t("permanentErasure")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
