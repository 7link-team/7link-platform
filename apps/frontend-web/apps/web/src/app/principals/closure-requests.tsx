import { PaginationPages } from "@workspace/ui/components/pagination"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
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
  const [readLoading, setReadLoading] = useState(true)
  const [error, setError] = useState("")
  const [operationError, setOperationError] = useState("")
  const [rejecting, setRejecting] = useState<ClosureRequest | null>(null)
  const [principal, setPrincipal] = useState<{ id: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<"pending" | "processing">("pending")
  const [action, setAction] = useState<"reject" | "process">("reject")
  useEffect(() => {
    let active = true
    void iamApi
      .closureRequests(offset, 50, status)
      .then(
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
      .finally(() => {
        if (active) setReadLoading(false)
      })
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
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("closureRequests")}
        description={t("closureRequestsDescription")}
      />

      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      <AutoTable
        loading={readLoading || (!items && !error)}
        fill
        onRefresh={() => {
          setReadLoading(true)
          setReload((value) => value + 1)
        }}
        rowOffset={offset}
        toolbar={
          <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
            <AutoForm
              key={status}
              className="w-full max-w-sm md:grid-cols-1"
              fields={{
                status: {
                  name: t("status"),
                  type: "select",
                  value: status,
                  config: {
                    options: {
                      pending: t("closureStatus_pending"),
                      processing: t("closureStatus_processing"),
                    },
                  },
                },
              }}
              onChange={(values) => {
                if (
                  (values.status === "pending" ||
                    values.status === "processing") &&
                  values.status !== status
                ) {
                  setItems(null)
                  setOffset(0)
                  setStatus(values.status)
                }
              }}
            />
          </div>
        }
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {Math.min(offset + 50, total)} / {total}
            </span>
            <PaginationPages
              page={Math.floor(offset / 50) + 1}
              totalPages={Math.max(1, Math.ceil(total / 50))}
              disabled={!items}
              onPageChange={(page) => {
                setItems(null)
                setOffset((page - 1) * 50)
              }}
            />
          </div>
        }
        pagination={false}
        emptyContent={error ? null : t("noClosureRequests")}
        data={items ?? []}
        rowKey="principal_id"
        columns={[
          {
            field: "principal_id",
            name: t("principalId"),
            type: "custom",
            render: (_value, item) => (
              <div>
                <h2 className="font-medium break-all">{item.principal_id}</h2>
                <p className="text-sm">
                  {item.requested_at &&
                    new Date(item.requested_at).toLocaleString(locale)}
                </p>
              </div>
            ),
          },
          {
            field: "actions",
            name: t("actions"),
            type: "actions",
            render: (_value, item) => (
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
            ),
          },
        ]}
      />

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
    </div>
  )
}
