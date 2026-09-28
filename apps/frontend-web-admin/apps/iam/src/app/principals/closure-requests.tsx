import { useEffect, useState } from "react"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type ClosureRequest } from "../../lib/iam-api"
import { PrincipalLifecycleDialog } from "./lifecycle-dialog"

export default function ClosureRequestsPage() {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [items, setItems] = useState<ClosureRequest[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState("")
  const [operationError, setOperationError] = useState("")
  const [rejecting, setRejecting] = useState<ClosureRequest | null>(null)
  const [principal, setPrincipal] = useState<{ id: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<"pending" | "processing">("pending")
  const [action, setAction] = useState<"reject" | "process">("reject")
  useEffect(() => {
    let active = true
    void iamApi.closureRequests(offset, 50, status).then(
      (result) => {
        if (active) {
          setItems(result.items)
          setTotal(result.total)
          setError("")
        }
      },
      (cause: Error) => {
        if (active) {
          setItems(null)
          setError(cause.message)
        }
      }
    )
    return () => {
      active = false
    }
  }, [offset, reload, status])
  const reject = async () => {
    if (!rejecting) return
    setBusy(true)
    setOperationError("")
    try {
      await (
        action === "process"
          ? iamApi.processClosureRequest
          : iamApi.rejectClosureRequest
      )(rejecting.principal_id, rejecting.version)
      setRejecting(null)
      setItems(null)
      if (action === "process") {
        setStatus("processing")
        setOffset(0)
      }
      setReload((value) => value + 1)
    } catch (cause) {
      setOperationError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <PageHeader
        title={t("closureRequests")}
        description={t("closureRequestsDescription")}
      />
      <label className="mb-4 flex items-center gap-3">
        {t("status")}
        <select
          className="min-h-11 rounded-md border bg-background px-3"
          value={status}
          onChange={(event) => {
            setItems(null)
            setOffset(0)
            if (
              event.target.value === "pending" ||
              event.target.value === "processing"
            )
              setStatus(event.target.value)
          }}
        >
          <option value="pending">{t("closureStatus_pending")}</option>
          <option value="processing">{t("closureStatus_processing")}</option>
        </select>
      </label>
      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!items ? (
        !error && <p role="status">{t("loading")}</p>
      ) : items.length === 0 ? (
        <p>{t("noClosureRequests")}</p>
      ) : (
        <ul className="space-y-3">
          {items.map((item) => (
            <li
              key={item.principal_id}
              className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-4"
            >
              <div className="min-w-0">
                <h2 className="font-medium break-all">{item.principal_id}</h2>
                <p className="text-sm">
                  {item.requested_at &&
                    new Date(item.requested_at).toLocaleString(locale)}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  onClick={() => setPrincipal({ id: item.principal_id })}
                >
                  {t("retentionErasure")}
                </Button>
                {item.status === "pending" && (
                  <Button
                    variant="outline"
                    onClick={() => {
                      setOperationError("")
                      setAction("process")
                      setRejecting(item)
                    }}
                  >
                    {t("processClosureRequest")}
                  </Button>
                )}
                {item.status === "pending" && (
                  <Button
                    variant="outline"
                    onClick={() => {
                      setOperationError("")
                      setAction("reject")
                      setRejecting(item)
                    }}
                  >
                    {t("rejectClosureRequest")}
                  </Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {items && (
        <div className="mt-4 flex items-center justify-between gap-3">
          <Button
            variant="outline"
            disabled={offset === 0}
            onClick={() => {
              setItems(null)
              setOffset(Math.max(0, offset - 50))
            }}
          >
            {t("previous")}
          </Button>
          <span>
            {Math.min(offset + 50, total)} / {total}
          </span>
          <Button
            variant="outline"
            disabled={offset + 50 >= total}
            onClick={() => {
              setItems(null)
              setOffset(offset + 50)
            }}
          >
            {t("next")}
          </Button>
        </div>
      )}
      <PrincipalLifecycleDialog
        principal={principal}
        iamApi={iamApi}
        onClose={() => {
          setPrincipal(null)
          setItems(null)
          setReload((value) => value + 1)
        }}
      />
      <Dialog
        open={Boolean(rejecting)}
        onOpenChange={(next) => {
          if (!next && !busy) setRejecting(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(
                action === "process"
                  ? "processClosureRequest"
                  : "rejectClosureRequest"
              )}
            </DialogTitle>
            <DialogDescription>
              {action === "process"
                ? `${rejecting?.principal_id}: ${t("processClosureDescription")}`
                : rejecting?.principal_id}
            </DialogDescription>
          </DialogHeader>
          {operationError && <Alert>{operationError}</Alert>}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setRejecting(null)}
            >
              {t("cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void reject()}
            >
              {t(
                action === "process"
                  ? "processClosureRequest"
                  : "rejectClosureRequest"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
