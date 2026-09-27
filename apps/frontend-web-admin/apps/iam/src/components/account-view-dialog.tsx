import { useState } from "react"
import { useLocation } from "react-router"
import { Eye, ExternalLink, Loader2 } from "lucide-react"
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
  openAccountViewTab,
  type AccountViewScope,
  type AccountViewMode,
} from "../lib/account-view"
import { iamApi } from "../lib/iam-api"

export function AccountViewDialog({
  principalID = "",
  label,
  preferredScope,
  preferredOwner,
  organization,
  disabled = false,
}: {
  principalID?: string
  label: string
  preferredScope?: AccountViewScope
  preferredOwner?: string
  organization?: { kind: "platform" | "tenant"; id: string }
  disabled?: boolean
}) {
  const t = useTranslations("iam")
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
        ...(organization
          ? { target_type: organization.kind }
          : { principal_id: principalID }),
        scope_type: scope,
        scope_id: scopeID,
        mode,
      }
      await openAccountViewTab(
        selection,
        location.pathname + location.search,
        scope === "tenant" ? scopeID : scope === "entity" ? tenantID : ""
      )
      setOpen(false)
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
        disabled={disabled}
        title={t(
          organization
            ? organization.kind === "platform"
              ? "enterOperatingPlatform"
              : "enterTenant"
            : "accountView"
        )}
        aria-label={
          organization
            ? organization.kind === "platform"
              ? `${t("enterOperatingPlatform")} ${label}`
              : t("enterTenantNamed", { name: label })
            : `${t("accountView")} ${label}`
        }
        onClick={() => {
          setMode("read_only")
          setError("")
          setOpen(true)
          if (organization) {
            setLoading(false)
            setScope(organization.kind)
            setScopeID(organization.id)
            setTenantID(organization.kind === "tenant" ? organization.id : "")
            return
          }
          setLoading(true)
          setWorkspaces([])
          setScope("personal")
          setScopeID(principalID)
          setTenantID("")
          void iamApi
            .accountViewWorkspaces(principalID)
            .then(
              (next) => {
                setWorkspaces(next)
                const selected =
                  next.find(
                    (item) =>
                      item.scope_type === preferredScope &&
                      item.scope_id === preferredOwner
                  ) ??
                  next.find((item) => item.scope_type === preferredScope) ??
                  next.find((item) => item.scope_type !== "personal") ??
                  next.find((item) => item.scope_type === "personal")
                if (!selected) return
                setScope(selected.scope_type)
                setScopeID(selected.scope_id)
                setTenantID(selected.tenant_id ?? "")
              },
              () => {
                setWorkspaces([])
                setError(t("accountViewOpenFailed"))
              }
            )
            .finally(() => setLoading(false))
        }}
      >
        {organization ? (
          <ExternalLink className="size-4" />
        ) : (
          <Eye className="size-4" />
        )}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(organization ? "organizationView" : "accountView")} · {label}
            </DialogTitle>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <div className="space-y-4">
            {!organization && (
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
                    setScopeID(
                      next.scope_type === "system" ? "1" : next.scope_id
                    )
                    setTenantID(next.tenant_id ?? "")
                  }}
                  disabled={loading}
                  options={workspaces.map((item) => ({
                    value: `${item.scope_type}:${item.scope_id}`,
                    label: `${t(`accountViewScope_${item.scope_type}`)} · ${item.name}`,
                  }))}
                />
              </div>
            )}
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
                (!organization && workspaces.length === 0) ||
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
