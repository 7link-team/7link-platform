import { useMemo, useRef, useState } from "react"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Link } from "react-router"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import { iamApi } from "../../lib/iam-api"
import { useRequestContract } from "../../lib/use-request-contract"
import type { components } from "../../lib/api-schema"

type Clause = components["schemas"]["WorkCriterion"]
function ConversionCriteria({
  value,
  onChange,
  disabled,
  labelId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const rows = (Array.isArray(value) ? value : []) as Clause[]
  return (
    <div role="group" aria-labelledby={labelId} className="space-y-3">
      {rows.map((row, i) => (
        <fieldset key={i} disabled={disabled} className="grid gap-2 border p-3">
          <legend>{t("workClauseNumber", { number: i + 1 })}</legend>
          <label className="grid gap-1">
            {t("conversationCriterion")}
            <Textarea
              required
              maxLength={2000}
              value={row.text}
              onChange={(e) =>
                onChange(
                  rows.map((c, n) =>
                    n === i ? { ...c, text: e.target.value } : c
                  )
                )
              }
            />
          </label>
          <label className="grid gap-1">
            {t("conversationVerification")}
            <Input
              required
              maxLength={2000}
              value={row.verification_method}
              onChange={(e) =>
                onChange(
                  rows.map((c, n) =>
                    n === i ? { ...c, verification_method: e.target.value } : c
                  )
                )
              }
            />
          </label>
          {rows.length > 1 && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => onChange(rows.filter((_, n) => n !== i))}
            >
              {t("conversationRemove")}
            </Button>
          )}
        </fieldset>
      ))}
      <Button
        type="button"
        variant="outline"
        disabled={disabled || rows.length >= 50}
        onClick={() =>
          onChange([...rows, { text: "", verification_method: "" }])
        }
      >
        {t("conversationAddCriterion")}
      </Button>
    </div>
  )
}
export function WorkConversion({
  channel,
  message,
  tenant,
  entity,
  onClose,
}: {
  channel: string
  message: string
  tenant: string
  entity: string
  onClose: () => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute(
        "post",
        "/channels/{channel_id}/messages/{message_id}/work-items",
        { path: { channel_id: channel, message_id: message } }
      ),
    [channel, message]
  )
  const metadata = useRequestContract(route)
  const [error, setError] = useState("")
  const [work, setWork] = useState("")
  const [busy, setBusy] = useState(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="max-h-[85svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("conversationConvert")}</DialogTitle>
          <DialogDescription>{t("conversationConvertHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {metadata.failed && (
          <Button variant="outline" onClick={metadata.retry}>
            {t("agentRefresh")}
          </Button>
        )}
        {work ? (
          <p role="status">
            <Link
              className="underline"
              to={`/admin/${tenant}/entities/${entity}/work-items`}
            >
              {t("conversationConverted")} · {work}
            </Link>
          </p>
        ) : (
          <AutoForm
            contract={metadata.contract}
            fields={{
              title: {
                name: t("workTitle"),
                type: "text",
                value: "",
                disabled: busy,
              },
              objective: {
                name: t("conversationObjective"),
                type: "textarea",
                value: "",
                disabled: busy,
              },
              accountable_principal_id: {
                name: t("conversationAccountable"),
                type: "text",
                value: "",
                disabled: busy,
              },
              criteria: {
                name: t("workCriteria"),
                type: "custom",
                component: ConversionCriteria,
                value: [{ text: "", verification_method: "" }],
                disabled: busy,
              },
            }}
            onSubmit={async (values) => {
              const body: components["schemas"]["ConvertWorkInput"] = {
                title: String(values.title),
                objective: String(values.objective),
                accountable_principal_id: String(
                  values.accountable_principal_id
                ),
                criteria: values.criteria as Clause[],
              }
              const identity = JSON.stringify(body)
              const key =
                attempt.current?.body === identity
                  ? attempt.current.key
                  : crypto.randomUUID()
              attempt.current = { body: identity, key }
              setBusy(true)
              setError("")
              try {
                const result = await iamApi.convertConversationMessage(
                  channel,
                  message,
                  body,
                  key
                )
                setWork(result.work_item_id ?? "")
              } catch (e) {
                setError((e as Error).message)
                throw e
              } finally {
                setBusy(false)
              }
            }}
          >
            <Button type="submit" disabled={busy || !metadata.contract}>
              {busy ? t("loading") : t("conversationConvert")}
            </Button>
          </AutoForm>
        )}
      </DialogContent>
    </Dialog>
  )
}
