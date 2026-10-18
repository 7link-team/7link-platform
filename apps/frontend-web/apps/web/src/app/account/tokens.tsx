import { PaginationPages } from "@workspace/ui/components/pagination"
import {
  AutoForm,
  type UseAutoFormReturn,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useCallback, useEffect, useState, useRef } from "react"
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
  const [readLoading, setReadLoading] = useState(true)
  const [error, setError] = useState("")
  const [operationError, setOperationError] = useState("")
  const [open, setOpen] = useState(false)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [tenant, setTenant] = useState("")
  const tenantSelection = useRef("")
  const tokenForm = useRef<UseAutoFormReturn>(null)
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
    void iamApi
      .personalTokens(offset)
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
  const updatePreparation = useCallback((values: Record<string, unknown>) => {
    const nextTenant = String(values.tenant_id ?? "")
    if (nextTenant !== tenantSelection.current) {
      tenantSelection.current = nextTenant
      setTenant(nextTenant)
      setSelectedPermissions([])
      setPermissions(null)
      setCatalogError("")
      tokenForm.current?.setValue("permissions", [])
      return
    }
    setSelectedPermissions(
      Array.isArray(values.permissions) ? (values.permissions as string[]) : []
    )
  }, [])

  const prepare = async (values: Record<string, unknown>) => {
    setBusy(true)
    setOperationError("")
    try {
      const result = await iamApi.preparePersonalToken({
        name: String(values.name),
        tenant_id: String(values.tenant_id),
        permissions: Array.isArray(values.permissions)
          ? (values.permissions as string[])
          : [],
        expires_at: new Date(String(values.expires_at)).toISOString(),
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
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader
        title={t("personalTokens")}
        description={t("personalTokensDescription")}
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
      <AutoTable
        fill
        toolbar={
          <Button
            onClick={() => {
              setOperationError("")
              tenantSelection.current = ""
              setTenant("")
              setPermissions(null)
              setSelectedPermissions([])
              setOpen(true)
            }}
          >
            {t("createPersonalToken")}
          </Button>
        }
        data={items ?? []}
        loading={readLoading || (!items && !error)}
        emptyContent={t("noPersonalTokens")}
        rowKey="id"
        pagination={false}
        rowOffset={offset}
        onRefresh={() => {
          setReadLoading(true)
          setReload((value) => value + 1)
        }}
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {Math.min(offset + 20, total)} / {total}
            </span>
            <PaginationPages
              page={Math.floor(offset / 20) + 1}
              totalPages={Math.max(1, Math.ceil(total / 20))}
              disabled={!items || !!error}
              onPageChange={(page) => {
                setItems(null)
                setOffset((page - 1) * 20)
              }}
            />
          </div>
        }
        columns={[
          {
            field: "name",
            name: t("name"),
            render: (_, token) => (
              <h2 className="font-medium break-all">{token.name}</h2>
            ),
          },
          {
            field: "tenant_id",
            name: t("tenant"),
            render: (_, token) => (
              <span className="break-all">{token.tenant_id}</span>
            ),
          },
          {
            field: "permissions",
            name: t("permissions"),
            render: (_, token) => (
              <span className="break-all">{token.permissions.join(", ")}</span>
            ),
          },
          {
            field: "created_at",
            name: t("createdAt"),
            render: (_, token) =>
              new Date(token.created_at).toLocaleString(locale),
          },
          {
            field: "expires_at",
            name: t("expiresAt"),
            render: (_, token) =>
              new Date(token.expires_at).toLocaleString(locale),
          },
          {
            field: "actions",
            type: "actions",
            name: t("actions"),
            render: (_, token) => (
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
            ),
          },
        ]}
      />

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
          <AutoForm
            formRef={tokenForm}
            onSubmit={prepare}
            onChange={updatePreparation}
            fields={{
              name: {
                name: t("name"),
                type: "text",
                required: true,
                config: { id: "token-name", maxLength: 128 },
              },
              tenant_id: {
                name: t("tenant"),
                type: "select",
                required: true,
                value: "",
                config: {
                  id: "token-tenant",
                  placeholder: t("selectTenant"),
                  options: tenants.map((item) => ({
                    value: item.id,
                    label: item.name,
                  })),
                },
              },
              expires_at: {
                name: t("expiresAt"),
                type: "text",
                required: true,
                config: { id: "token-expiry", type: "datetime-local" },
              },
              permissions: {
                name: t("permissions"),
                type: "checkbox-group",
                value: [],
                config: {
                  options: (permissions ?? []).map((permission) => ({
                    value: permission.code,
                    label: permission.code,
                  })),
                },
              },
            }}
          >
            {tenant && !permissions && !catalogError && (
              <p role="status">{t("loading")}</p>
            )}
            {permissions?.length === 0 && <p>{t("noTokenPermissions")}</p>}
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
          </AutoForm>
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
    </div>
  )
}
