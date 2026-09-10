import { useCallback, useEffect, useState, type FormEvent } from "react"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { Checkbox } from "@workspace/ui/components/checkbox"
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
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAuth } from "../../components/auth"
import {
  iamApi,
  type PersonalToken,
  type PersonalTokenRequest,
  type Permission,
  type Tenant,
} from "../../lib/iam-api"

const pendingKey = "iam.personal-token-preparation"
type Preparation = { request: PersonalTokenRequest; target: string }

export default function PersonalTokensPage() {
  const t = useTranslations("iam")
  const locale = useLocale()
  const { session } = useAuth()
  const [items, setItems] = useState<PersonalToken[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState("")
  const [operationError, setOperationError] = useState("")
  const [open, setOpen] = useState(false)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [tenant, setTenant] = useState("")
  const [permissions, setPermissions] = useState<Permission[] | null>(null)
  const [selectedPermissions, setSelectedPermissions] = useState<string[]>([])
  const [catalogError, setCatalogError] = useState("")
  const [catalogReload, setCatalogReload] = useState(0)
  const [busy, setBusy] = useState(false)
  const [revoking, setRevoking] = useState<PersonalToken | null>(null)
  const [secret, setSecret] = useState("")
  const [prepared, setPrepared] = useState<Preparation | null>(() => {
    const value = sessionStorage.getItem(pendingKey)
    if (!value) return null
    try {
      const saved = JSON.parse(value)
      if (
        saved.subject === session?.subject &&
        typeof saved.target === "string" &&
        saved.request
      )
        return saved
    } catch {
      /* A malformed or another account's continuation is discarded. */
    }
    sessionStorage.removeItem(pendingKey)
    return null
  })
  useEffect(() => {
    let active = true
    void iamApi.personalTokens(offset).then(
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
  }, [offset, reload])
  useEffect(() => {
    if (!open) return
    let active = true
    void iamApi.myTenants().then(
      (next) => {
        if (active) {
          setTenants(next)
          setCatalogError("")
        }
      },
      (cause: Error) => {
        if (active) setCatalogError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [open, catalogReload])
  useEffect(() => {
    if (!tenant) return
    let active = true
    void iamApi.personalTokenPermissions(tenant).then(
      (next) => {
        if (active) {
          setPermissions(next)
          setCatalogError("")
        }
      },
      (cause: Error) => {
        if (active) setCatalogError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [tenant, catalogReload])
  const clearPrepared = () => {
    sessionStorage.removeItem(pendingKey)
    setPrepared(null)
  }
  const create = useCallback(
    async (proof: string) => {
      if (!prepared) return
      const token = await iamApi.createPersonalToken({
        ...prepared.request,
        proof,
      })
      if (!token.token) throw new Error("invalid_response")
      sessionStorage.removeItem(pendingKey)
      setPrepared(null)
      setSecret(token.token)
      setItems(null)
      setOffset(0)
      setReload((value) => value + 1)
    },
    [prepared]
  )
  const prepare = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setOperationError("")
    try {
      const result = await iamApi.preparePersonalToken({
        name: String(form.get("name")),
        tenant_id: tenant,
        permissions: selectedPermissions,
        expires_at: new Date(String(form.get("expires_at"))).toISOString(),
      })
      sessionStorage.setItem(
        pendingKey,
        JSON.stringify({ ...result, subject: session?.subject })
      )
      setPrepared(result)
      setOpen(false)
    } catch (cause) {
      setOperationError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const revoke = async () => {
    if (!revoking) return
    setBusy(true)
    setOperationError("")
    try {
      await iamApi.revokePersonalToken(revoking.id)
      setRevoking(null)
      setItems(null)
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
        title={t("personalTokens")}
        description={t("personalTokensDescription")}
        action={
          <Button
            onClick={() => {
              setOperationError("")
              setTenant("")
              setPermissions(null)
              setSelectedPermissions([])
              setOpen(true)
            }}
          >
            {t("createPersonalToken")}
          </Button>
        }
      />
      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {operationError && !open && !revoking && !secret && (
        <Alert>{operationError}</Alert>
      )}
      {!items ? (
        !error && <p role="status">{t("loading")}</p>
      ) : items.length === 0 ? (
        <p>{t("noPersonalTokens")}</p>
      ) : (
        <ul className="space-y-3">
          {items.map((token) => (
            <li key={token.id} className="rounded-md border p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="font-medium break-all">{token.name}</h2>
                <Button
                  variant="outline"
                  disabled={Boolean(token.revoked_at)}
                  onClick={() => {
                    setOperationError("")
                    setRevoking(token)
                  }}
                >
                  {token.revoked_at ? t("revoked") : t("revoke")}
                </Button>
              </div>
              <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm [&>dd]:break-all">
                <dt>{t("tenant")}</dt>
                <dd>{token.tenant_id}</dd>
                <dt>{t("permissions")}</dt>
                <dd>{token.permissions.join(", ")}</dd>
                <dt>{t("createdAt")}</dt>
                <dd>{new Date(token.created_at).toLocaleString(locale)}</dd>
                <dt>{t("expiresAt")}</dt>
                <dd>{new Date(token.expires_at).toLocaleString(locale)}</dd>
              </dl>
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
              setOffset(Math.max(0, offset - 20))
            }}
          >
            {t("previous")}
          </Button>
          <span>
            {offset + 1 > total ? 0 : offset + 1}–{Math.min(offset + 20, total)}{" "}
            / {total}
          </span>
          <Button
            variant="outline"
            disabled={offset + 20 >= total}
            onClick={() => {
              setItems(null)
              setOffset(offset + 20)
            }}
          >
            {t("next")}
          </Button>
        </div>
      )}
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!busy) setOpen(next)
        }}
      >
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t("createPersonalToken")}</DialogTitle>
            <DialogDescription>
              {t("personalTokensDescription")}
            </DialogDescription>
          </DialogHeader>
          {operationError && <Alert>{operationError}</Alert>}
          {catalogError && (
            <Alert>
              <p>{t.has(catalogError) ? t(catalogError) : catalogError}</p>
              <Button onClick={() => setCatalogReload((value) => value + 1)}>
                {t("retryNow")}
              </Button>
            </Alert>
          )}
          <form onSubmit={prepare} className="grid gap-3">
            <Label htmlFor="token-name">{t("name")}</Label>
            <Input id="token-name" name="name" required maxLength={128} />
            <Label htmlFor="token-tenant">{t("tenant")}</Label>
            <select
              id="token-tenant"
              required
              value={tenant}
              className="min-h-11 rounded-md border bg-background px-3"
              onChange={(event) => {
                setTenant(event.target.value)
                setSelectedPermissions([])
                setPermissions(null)
                setCatalogError("")
              }}
            >
              <option value="">{t("selectTenant")}</option>
              {tenants.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
            <Label htmlFor="token-expiry">{t("expiresAt")}</Label>
            <Input
              id="token-expiry"
              name="expires_at"
              type="datetime-local"
              required
            />
            <fieldset className="grid gap-2">
              <legend className="mb-2 text-sm font-medium">
                {t("permissions")}
              </legend>
              {tenant && !permissions && !catalogError && (
                <p role="status">{t("loading")}</p>
              )}
              {permissions?.length === 0 && <p>{t("noTokenPermissions")}</p>}
              {permissions?.map((permission) => (
                <label
                  key={permission.code}
                  className="flex min-h-11 items-center gap-3 break-all"
                >
                  <Checkbox
                    checked={selectedPermissions.includes(permission.code)}
                    onCheckedChange={(checked) =>
                      setSelectedPermissions((previous) =>
                        checked
                          ? [...previous, permission.code]
                          : previous.filter((code) => code !== permission.code)
                      )
                    }
                  />
                  {permission.code}
                </label>
              ))}
            </fieldset>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setOpen(false)}
                disabled={busy}
              >
                {t("cancel")}
              </Button>
              <Button
                type="submit"
                disabled={
                  busy ||
                  Boolean(catalogError) ||
                  selectedPermissions.length === 0
                }
              >
                {t("continue")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {prepared && (
        <ReauthenticationDialog
          purpose="personal_token_create"
          target={prepared.target}
          onVerified={create}
          onClose={clearPrepared}
        />
      )}
      <Dialog
        open={Boolean(secret)}
        onOpenChange={(next) => {
          if (!next) setSecret("")
        }}
      >
        <DialogContent className="grid">
          <DialogHeader>
            <DialogTitle>{t("personalTokenCreated")}</DialogTitle>
            <DialogDescription>{t("personalTokenOnce")}</DialogDescription>
          </DialogHeader>
          {operationError && <Alert>{operationError}</Alert>}
          <code
            className="rounded-md bg-muted p-3 break-all"
            data-testid="personal-token-secret"
          >
            {secret}
          </code>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                void navigator.clipboard
                  .writeText(secret)
                  .catch(() => setOperationError(t("clipboardRejected")))
              }}
            >
              {t("copy")}
            </Button>
            <Button onClick={() => setSecret("")}>{t("close")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(revoking)}
        onOpenChange={(next) => {
          if (!next && !busy) setRevoking(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("revokePersonalToken")}</DialogTitle>
            <DialogDescription>{revoking?.name}</DialogDescription>
          </DialogHeader>
          {operationError && <Alert>{operationError}</Alert>}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setRevoking(null)}
            >
              {t("cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void revoke()}
            >
              {t("revoke")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
