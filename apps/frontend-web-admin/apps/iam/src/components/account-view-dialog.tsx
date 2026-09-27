import { useState } from "react"
import { useLocation, useNavigate } from "react-router"
import { Eye, Loader2 } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import { Alert } from "./alert"
import {
  activateAccountView,
  issueAccountView,
  type AccountViewScope,
  type AccountViewMode,
} from "../lib/account-view"
import { iamApi } from "../lib/iam-api"

export function AccountViewDialog({
  principalID,
  label,
}: {
  principalID: string
  label: string
}) {
  const t = useTranslations("iam")
  const navigate = useNavigate()
  const location = useLocation()
  const [open, setOpen] = useState(false)
  const [scope, setScope] = useState<AccountViewScope>("personal")
  const [scopeID, setScopeID] = useState(principalID)
  const [tenantID, setTenantID] = useState("")
  const [workspaces, setWorkspaces] = useState<
    Awaited<ReturnType<typeof iamApi.accountViewWorkspaces>>
  >([])
  const [loading, setLoading] = useState(false)
  const [mode, setMode] = useState<AccountViewMode>("read_only")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const enter = async () => {
    setBusy(true)
    setError("")
    try {
      const selection = {
        principal_id: principalID,
        scope_type: scope,
        scope_id: scopeID,
        mode,
      }
      const issued = await issueAccountView(selection)
      activateAccountView(
        selection,
        issued,
        location.pathname + location.search,
        scope === "tenant" ? scopeID : scope === "entity" ? tenantID : ""
      )
      setOpen(false)
      const path =
        scope === "personal"
          ? "/me/profile"
          : scope === "system"
            ? "/system/accounts"
            : scope === "platform"
              ? `/operating-platforms/${encodeURIComponent(scopeID)}`
              : scope === "entity"
                ? `/tenants/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(scopeID)}/overview`
                : `/tenants/${encodeURIComponent(scopeID)}`
      void navigate(path)
    } catch {
      setError(t("accountViewOpenFailed"))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button
        size="icon"
        variant="ghost"
        title={t("accountView")}
        aria-label={`${t("accountView")} ${label}`}
        onClick={() => {
          setMode("read_only")
          setError("")
          setOpen(true)
          setLoading(true)
          setScope("personal")
          setScopeID(principalID)
          setTenantID("")
          void iamApi
            .accountViewWorkspaces(principalID)
            .then(setWorkspaces, () => {
              setWorkspaces([])
              setError(t("accountViewOpenFailed"))
            })
            .finally(() => setLoading(false))
        }}
      >
        <Eye className="size-4" />
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t("accountView")} · {label}
            </DialogTitle>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="account-view-scope">
                {t("accountViewScope")}
              </Label>
              <SimpleSelect
                id="account-view-scope"
                value={`${scope}:${scopeID}`}
                onValueChange={(value) => {
                  const next = workspaces.find(
                    (item) => `${item.scope_type}:${item.scope_id}` === value
                  )
                  if (!next) return
                  setScope(next.scope_type)
                  setScopeID(next.scope_type === "system" ? "1" : next.scope_id)
                  setTenantID(next.tenant_id ?? "")
                }}
                disabled={loading}
                options={workspaces.map((item) => ({
                  value: `${item.scope_type}:${item.scope_id}`,
                  label: `${t(`accountViewScope_${item.scope_type}`)} · ${item.name}`,
                }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="account-view-mode">{t("accountViewMode")}</Label>
              <SimpleSelect
                id="account-view-mode"
                value={mode}
                onValueChange={(value) => setMode(value as AccountViewMode)}
                options={[
                  { value: "read_only", label: t("accountViewReadOnly") },
                  { value: "read_write", label: t("accountViewWritable") },
                ]}
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setOpen(false)}
              disabled={busy}
            >
              {t("cancel")}
            </Button>
            <Button
              onClick={() => {
                void enter()
              }}
              disabled={
                busy ||
                loading ||
                workspaces.length === 0 ||
                !scopeID.trim() ||
                (scope === "entity" && !tenantID.trim())
              }
            >
              {busy ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Eye className="size-4" />
              )}
              {t("accountViewOpen")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
