import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useEffect, useState } from "react"
import { Link, Navigate, useLocation, useNavigate, useNavigation } from "react-router"
import { Building2, Landmark, Network, Plus, LogOut, Users } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { useAuth } from "../../components/auth"
import { Alert } from "../../components/alert"
import { SessionVerification } from "../../components/session-verification"
import { PendingRoute } from "../../components/pending-route"
import { authEntrance, entranceLoginPath, workspaceInvitationToken } from "../../lib/auth-entrance"
import { iamApi, setTenant, type Tenant } from "../../lib/iam-api"

export default function EntranceWorkspaces() {
  const location = useLocation()
  const { session } = useAuth()
  return (
    <WorkspaceOptions
      key={`${location.pathname}${location.search}:${session?.subject ?? ""}`}
    />
  )
}

function WorkspaceOptions() {
  const t = useTranslations("iam")
  const location = useLocation()
  const navigate = useNavigate()
  const navigation = useNavigation()
  const entry = authEntrance(location.pathname.split("/")[1])
  const { status, session, logout } = useAuth()
  const [items, setItems] = useState<Tenant[] | null>(null)
  const [system, setSystem] = useState(false)
  const [registration, setRegistration] = useState(false)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [reload, setReload] = useState(0)
  const [joining, setJoining] = useState(false)
  useEffect(() => {
    if (status !== "authenticated" || !entry) return
    let active = true
    const load = async () => {
      if (entry === "system") {
        const permissions = await iamApi.systemPermissions()
        if (active) {
          setSystem(permissions.length > 0)
          setItems([])
        }
      } else if (entry === "platform") {
        const platforms = await iamApi.myOperatingPlatforms()
        if (active)
          setItems(platforms.filter((item) => item.status === "active"))
      } else if (entry === "tenant") {
        const [tenants, policy] = await Promise.all([
          iamApi.myTenants(),
          iamApi.tenantRegistrationPolicy().catch((cause: Error) => {
            if (active) setError(cause.message)
            return { enabled: false }
          }),
        ])
        if (active) {
          setItems(tenants.filter((item) => item.status === "active"))
          setRegistration(policy.enabled)
          setCreating(
            policy.enabled &&
              new URLSearchParams(location.search).get("create") === "1"
          )
        }
      } else if (active) setItems([])
    }
    void load().catch((cause: Error) => {
      if (active) setError(cause.message)
    })
    return () => {
      active = false
    }
  }, [entry, status, session?.subject, location.search, reload])

  useEffect(() => {
    if (status !== "authenticated" || items?.length !== 1 || creating) return
    const selected = items[0]!
    // The tenant route decides which of its perspectives this caller gets, so a
    // chosen tenant is addressed directly: routing this through the workspace
    // home would send an operator with system authority to /system instead of
    // the tenant they chose.
    navigate(
      entry === "platform"
        ? `/platform/${selected.id}`
        : `/admin/${selected.id}`,
      { replace: true }
    )
  }, [status, items, creating, entry, navigate])

  if (status === "anonymous")
    return <Navigate to={entranceLoginPath(entry)} replace />
  if (status !== "authenticated") return <SessionVerification />
  if (entry === "user") return <Navigate to="/" replace />
  if (items && entry === "system" && system)
    return <Navigate to="/system" replace />
  if ((items?.length === 1 && !creating) || navigation.state !== "idle") {
    return <PendingRoute />
  }
  const createTenant = async (values: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      const tenant = await iamApi.registerTenant({
        name: String(values.name ?? "").trim(),
        slug: String(values.slug ?? "").trim(),
      })
      setTenant(tenant.id)
      navigate(`/admin/${tenant.id}`, { replace: true })
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const joinTenant = async (values: Record<string, unknown>) => {
    const token = workspaceInvitationToken(String(values.invitation ?? ""))
    if (!token) { setError(t("workspaceInvitationInvalid")); return }
    setBusy(true)
    setError("")
    try {
      const accepted = await iamApi.joinInvitation(token)
      setTenant(accepted.tenant_id)
      navigate(`/admin/${encodeURIComponent(accepted.tenant_id)}`, { replace: true })
    } catch (cause) {
      setError((cause as Error).message)
    } finally { setBusy(false) }
  }
  const Icon =
    entry === "platform" ? Network : entry === "system" ? Landmark : Building2
  return (
    <main className="mx-auto w-full max-w-xl px-4 py-10">
      <header className="mt-6 flex items-start justify-between gap-3 border-b pb-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold">
            <Icon className="size-5" />
            {t("selectEntranceWorkspace")}
          </h1>
          <p className="mt-2 text-sm break-all text-muted-foreground">
            {session?.preferred_username || session?.email || t("account")}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          title={t("logout")}
          aria-label={t("logout")}
          onClick={() =>
            void logout().catch((cause: Error) => setError(cause.message))
          }
        >
          <LogOut />
        </Button>
      </header>
      {error && (
        <div className="mt-4">
          <Alert>
            <p>{error}</p>
            <Button variant="outline" onClick={() => { setError(""); setReload(value => value + 1) }}>{t("retryNow")}</Button>
          </Alert>
        </div>
      )}
      {!items && !error && (
        <p className="mt-6 text-sm text-muted-foreground">{t("loading")}</p>
      )}
      {items && !creating && (
        <div className="mt-4 divide-y">
          {items.map((item) => (
            <Link
              key={item.id}
              className="flex items-center gap-3 py-4 hover:text-primary"
              to={
                entry === "platform"
                  ? `/platform/${item.id}`
                  : `/admin/${item.id}`
              }
            >
              <Icon className="size-5" />
              <span>{item.name}</span>
            </Link>
          ))}
          {items.length === 0 && entry !== "tenant" && (
            <p className="py-4 text-sm text-muted-foreground">
              {t("entranceAccessDenied")}
            </p>
          )}
        </div>
      )}
      {entry === "tenant" && items && !creating && (
        <section className="mt-6 space-y-5" aria-label={t("workspaceGetStarted")}>
          {items.length === 0 && <div className="space-y-2">
            <h2 className="text-lg font-semibold">{t("workspaceGetStarted")}</h2>
            <p className="text-sm text-muted-foreground">{t("workspaceGetStartedDescription")}</p>
          </div>}
          <div className="grid gap-6 sm:grid-cols-2">
            <div className="space-y-3 border-t pt-4">
              <h3 className="font-medium">{t("workspaceCreate")}</h3>
              <p className="text-sm text-muted-foreground">{t(registration ? "workspaceCreateDescription" : "workspaceCreateDisabled")}</p>
              <Button disabled={!registration} onClick={() => setCreating(true)}><Plus />{t("workspaceCreate")}</Button>
            </div>
            <div className="space-y-3 border-t pt-4">
              <h3 className="font-medium">{t("workspaceJoin")}</h3>
              <p className="text-sm text-muted-foreground">{t("workspaceJoinDescription")}</p>
              <Button variant="outline" onClick={() => setJoining(value => !value)} aria-expanded={joining}><Users />{t("workspaceJoin")}</Button>
            </div>
          </div>
          {joining && <div className="space-y-3 rounded-md border p-4">
            <p className="text-sm">{t("workspaceJoinInstructions")}</p>
            <AutoForm className="md:grid-cols-1 [&>*]:col-span-1" onSubmit={joinTenant} fields={{invitation: {name: t("workspaceInvitation"), type: "text", required: true, config: {id: "workspace-invitation", maxLength: 2048}}}}>
              <Button type="submit" disabled={busy}>{busy ? t("accepting") : t("workspaceJoin")}</Button>
            </AutoForm>
            <Button variant="outline" onClick={() => { setError(""); setReload(value => value + 1) }}>{t("workspaceRefreshMemberships")}</Button>
          </div>}
        </section>
      )}
      {creating && (
        <section className="mt-6 space-y-4">
          <h2 className="text-lg font-semibold">{t("workspaceCreate")}</h2>
          <AutoForm
            className="mt-6 md:grid-cols-1 [&>*]:col-span-1"
            onSubmit={createTenant}
            fields={{
              name: {
                name: t("name"),
                type: "text",
                required: true,
                config: { id: "tenant-registration-name", maxLength: 128 },
              },
              slug: {
                name: t("identifier"),
                type: "text",
                required: true,
                config: {
                  id: "tenant-registration-slug",
                  minLength: 3,
                  maxLength: 63,
                  pattern: "[a-z0-9][a-z0-9-]*[a-z0-9]",
                },
              },
            }}
          >
            <div className="flex gap-2">
              <Button type="submit" disabled={busy}>
                {t("workspaceCreate")}
              </Button>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setCreating(false)}
              >
                {t("cancel")}
              </Button>
            </div>
          </AutoForm>
        </section>
      )}
      {entry === "tenant" && <Link to="/user/profile" className="mt-8 inline-flex min-h-11 items-center text-sm underline underline-offset-4">{t("account")}</Link>}
    </main>
  )
}
