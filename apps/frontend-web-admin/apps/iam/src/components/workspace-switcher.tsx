import { useState } from "react"
import {
  Building2,
  Check,
  ChevronRight,
  ChevronsUpDown,
  Network,
  Landmark,
  UserRound,
} from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@workspace/ui/components/popover"
import { Alert } from "./alert"
import type { Entity, Tenant } from "../lib/iam-api"

export function WorkspaceSwitcher({
  tenantID,
  entityID,
  tenants,
  entities,
  error,
  tenantError = "",
  onRetryTenants,
  onTenant,
  onEntity,
  platformPath = "",
  platformView = false,
  onPlatform,
  onPersonal,
  operatingPlatforms = [],
  onOperatingPlatform,
  operatingPlatformID = "",
}: {
  tenantID: string
  entityID: string
  tenants: Pick<Tenant, "id" | "slug" | "name" | "status">[] | null
  entities: Entity[] | null
  error: string
  tenantError?: string
  onRetryTenants?: () => void
  onTenant: (id: string) => void
  onEntity: (id: string) => void
  platformPath?: string
  platformView?: boolean
  onPlatform?: () => void
  onPersonal?: () => void
  operatingPlatforms?: Tenant[]
  onOperatingPlatform?: (id: string) => void
  operatingPlatformID?: string
}) {
  const t = useTranslations("iam")
  const [open, setOpen] = useState(false)
  const tenantName =
    tenants?.find((tenant) => tenant.id === tenantID)?.name ||
    tenantID ||
    t("selectTenant")
  const entityName =
    entities?.find((entity) => entity.id === entityID)?.name || entityID
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
      }}
    >
      <PopoverTrigger
        render={
          <Button
            variant="outline"
            className="h-11 max-w-52 min-w-0 justify-start sm:max-w-80"
            aria-label={t("switchWorkspace")}
          />
        }
      >
        {platformView ? (
          <Landmark className="shrink-0" />
        ) : (
          <Building2 className="shrink-0" />
        )}
        <span className="min-w-0 truncate">
          {operatingPlatformID
            ? operatingPlatforms.find(
                (platform) => platform.id === operatingPlatformID
              )?.name || `${t("operatingPlatforms")} · ${operatingPlatformID}`
            : platformView
              ? t("platformManagement")
              : `${tenantName} / ${entityID ? entityName : t("tenantPerspective")}`}
        </span>
        <ChevronsUpDown className="shrink-0" />
      </PopoverTrigger>
      <PopoverContent
        data-workspace-switcher
        align="start"
        className="max-h-[min(75svh,var(--available-height))] w-[min(640px,calc(100vw-24px))] overflow-y-auto"
        aria-label={t("switchWorkspace")}
        aria-describedby={undefined}
      >
        <div className="mb-4 flex flex-wrap gap-2 border-b pb-3">
          {platformPath && (
            <Button
              variant={platformView ? "secondary" : "outline"}
              onClick={() => {
                onPlatform?.()
                setOpen(false)
              }}
            >
              <Landmark />
              {t("platformManagement")}
            </Button>
          )}
          <Button
            variant="outline"
            onClick={() => {
              onPersonal?.()
              setOpen(false)
            }}
          >
            <UserRound />
            {t("account")}
          </Button>
        </div>
        {operatingPlatforms.length > 0 && (
          <section className="mb-4 border-b pb-3">
            <h2 className="mb-2 text-sm font-semibold">
              {t("operatingPlatforms")}
            </h2>
            <div className="flex flex-wrap gap-2">
              {operatingPlatforms.map((platform) => (
                <Button
                  key={platform.id}
                  variant="outline"
                  onClick={() => {
                    onOperatingPlatform?.(platform.id)
                    setOpen(false)
                  }}
                >
                  <Landmark className="size-4" />
                  {platform.name}
                </Button>
              ))}
            </div>
          </section>
        )}
        <div className="grid min-h-64 grid-cols-1 gap-4 sm:grid-cols-[1fr_1.4fr]">
          <section
            className="min-w-0 sm:border-e sm:pe-4"
            aria-label={t("currentTenant")}
          >
            <h2 className="mb-2 text-sm font-semibold">{t("currentTenant")}</h2>
            {tenantError ? (
              <Alert>
                <p>{tenantError}</p>
                <Button variant="outline" onClick={onRetryTenants}>
                  {t("retryNow")}
                </Button>
              </Alert>
            ) : tenants ? (
              <div className="max-h-60 space-y-1 overflow-y-auto">
                {tenants.length === 0 && (
                  <p className="text-sm">{t("noAvailableTenants")}</p>
                )}
                {tenants
                  .filter((tenant) => tenant.status === "active")
                  .map((tenant) => (
                    <button
                      key={tenant.id}
                      type="button"
                      aria-pressed={tenant.id === tenantID}
                      onClick={() => {
                        onTenant(tenant.id)
                        setOpen(false)
                      }}
                      className={`flex min-h-11 w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm ${tenant.id === tenantID ? "bg-accent font-medium" : "hover:bg-muted"}`}
                    >
                      <Building2 className="size-4 shrink-0" />
                      <span className="min-w-0 break-words">
                        {tenant.name}
                        <span className="block text-xs text-muted-foreground">
                          {tenant.slug}
                        </span>
                      </span>
                      <ChevronRight className="ms-auto size-4 shrink-0" />
                    </button>
                  ))}
              </div>
            ) : (
              <p role="status" className="text-sm">
                {t("loading")}
              </p>
            )}
          </section>
          <section className="min-w-0" aria-label={t("entityPerspective")}>
            <h2 className="mb-2 text-sm font-semibold break-words">
              {tenantName}
            </h2>
            <button
              type="button"
              disabled={!tenantID}
              onClick={() => {
                onEntity("")
                setOpen(false)
              }}
              className="mb-3 flex min-h-11 w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm hover:bg-accent"
            >
              <Building2 className="size-4 shrink-0" />
              <span>{t("tenantPerspective")}</span>
              {!platformView && !entityID && (
                <Check className="ms-auto size-4 shrink-0" />
              )}
            </button>
            {!platformView && (
              <>
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                  {t("entityPerspective")}
                </h3>
                {error ? (
                  <Alert>{error}</Alert>
                ) : !entities ? (
                  <p role="status" className="text-sm">
                    {t("loading")}
                  </p>
                ) : (
                  <div className="max-h-60 space-y-1 overflow-y-auto">
                    {entities
                      .filter((entity) => entity.status === "active")
                      .map((entity) => (
                        <button
                          key={entity.id}
                          type="button"
                          onClick={() => {
                            onEntity(entity.id)
                            setOpen(false)
                          }}
                          className="flex min-h-11 w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm hover:bg-accent"
                        >
                          <Network className="size-4 shrink-0" />
                          <span className="min-w-0 break-words">
                            {entity.name}
                            {entity.parent_id && (
                              <span className="block text-xs text-muted-foreground">
                                {entities.find(
                                  (parent) => parent.id === entity.parent_id
                                )?.name ?? entity.parent_id}
                              </span>
                            )}
                          </span>
                          {entityID === entity.id && (
                            <Check className="ms-auto size-4 shrink-0" />
                          )}
                        </button>
                      ))}
                    {!entities.some((entity) => entity.status === "active") && (
                      <p className="p-3 text-sm text-muted-foreground">
                        {t("noEntities")}
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      </PopoverContent>
    </Popover>
  )
}
