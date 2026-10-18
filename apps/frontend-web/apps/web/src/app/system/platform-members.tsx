import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useCallback, useEffect, useState } from "react"
import { useParams } from "react-router"
import { Plus, UserRoundX, UserRoundCheck } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { iamApi, type Member } from "../../lib/iam-api"

export default function PlatformMembersPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { platformID = "" } = useParams<{ platformID: string }>()
  const [items, setItems] = useState<Member[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const refresh = useCallback(async () => {
    try {
      setItems(await iamApi.members())
      setError("")
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
    }
  }, [refresh])
  const save = async (data: Record<string, unknown>) => {
    setBusy(true)
    try {
      await iamApi.addOperatingMember(platformID, {
        subject: String(data.principal_id),
        display_name: String(data.display_name),
        email: String(data.email),
      })
      setOpen(false)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const toggle = async (item: Member) => {
    setBusy(true)
    try {
      await iamApi.updateMember(item.subject, {
        status: item.status === "active" ? "disabled" : "active",
      })
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader title={t("platformMembers")} />
      {error && <Alert>{error}</Alert>}
      <AutoTable
        loading={loading}
        fill
        onRefresh={refresh}
        searchable={(member) =>
          [
            member.display_name,
            member.email,
            member.subject,
            member.status,
          ].join(" ")
        }
        toolbar={
          <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
            {allowed("platform_member_create") && (
              <Button onClick={() => setOpen(true)}>
                <Plus className="size-4" />
                {t("addPlatformMember")}
              </Button>
            )}
          </div>
        }
        emptyContent={null}
        data={items}
        rowKey={(item) => item.subject}
        columns={[
          {
            field: "displayName",
            name: t("displayName"),
            type: "custom",
            render: (_value, item) => (
              <div>{item.display_name || item.subject}</div>
            ),
          },
          {
            field: "email",
            name: t("email"),
            type: "text",
          },
          {
            field: "status",
            name: t("status"),
            type: "text",
          },
          {
            field: "actions",
            name: t("actions"),
            type: "actions",
            render: (_value, item) => (
              <div>
                {allowed("platform_member_update") && (
                  <Button
                    size="icon"
                    variant="ghost"
                    title={t(item.status === "active" ? "disable" : "enable")}
                    aria-label={t(
                      item.status === "active" ? "disable" : "enable"
                    )}
                    disabled={busy}
                    onClick={() => {
                      void toggle(item)
                    }}
                  >
                    {item.status === "active" ? (
                      <UserRoundX className="size-4" />
                    ) : (
                      <UserRoundCheck className="size-4" />
                    )}
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("addPlatformMember")}</DialogTitle>
          </DialogHeader>
          <AutoForm
            id="system-platform-members-form"
            fields={{
              principal_id: {
                name: t("principalID"),
                type: "text",
                value: "",
                required: true,
                config: { id: "platform-member-id" },
              },
              display_name: { name: t("displayName"), type: "text", value: "" },
              email: { name: t("email"), type: "email", value: "" },
            }}
            onSubmit={save}
          >
            <DialogFooter>
              <Button
                variant="outline"
                type="button"
                onClick={() => setOpen(false)}
              >
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {t("save")}
              </Button>
            </DialogFooter>
          </AutoForm>
        </DialogContent>
      </Dialog>
    </div>
  )
}
