import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useMemo, useState } from "react"
import { Pencil, Trash2 } from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  AutoTable,
  type ColumnConfig,
} from "@workspace/ui/components/auto-table"
import { AutoForm } from "@workspace/ui/components/auto-form"
import type { Member, MembershipWindowInput } from "../lib/iam-api"
import {
  membershipState,
  membershipWindow,
  toDateTimeLocal,
  toISOString,
  type MembershipDates,
} from "../lib/membership-presentation"

export interface DirectoryMember extends MembershipDates {
  principal_id: string
  display_name: string
  email?: string
}

interface DirectoryMembersDialogProps {
  open: boolean
  itemName: string
  directoryLabel: string
  scheduleLabel: string
  formID: string
  inputPrefix: string
  members: DirectoryMember[]
  tenantMembers: Member[]
  busy: boolean
  computed?: boolean
  onRefresh?: () => Promise<void>
  onClose: () => void
  onError: (message: string) => void
  onPut: (principalID: string, input: MembershipWindowInput) => Promise<boolean>
  onDelete: (member: DirectoryMember) => Promise<boolean>
}

export function DirectoryMembersDialog({
  open,
  itemName,
  directoryLabel,
  scheduleLabel,
  formID,
  inputPrefix,
  members,
  tenantMembers,
  busy,
  computed = false,
  onClose,
  onRefresh,
  onError,
  onPut,
  onDelete,
}: DirectoryMembersDialogProps) {
  const t = useTranslations("iam")
  const locale = useLocale()

  const firstAvailable = () =>
    tenantMembers.find(
      (member) =>
        member.status === "active" &&
        !members.some((item) => item.principal_id === member.subject)
    )?.subject ?? ""
  const [formReset, setFormReset] = useState(0)
  const [editing, setEditing] = useState<DirectoryMember | null>(null)
  const [selectedPrincipal, setSelectedPrincipal] = useState(firstAvailable)
  const availableMembers = useMemo(() => {
    const assigned = new Set(members.map((member) => member.principal_id))
    return tenantMembers.filter(
      (member) =>
        member.status === "active" &&
        (!assigned.has(member.subject) ||
          member.subject === editing?.principal_id)
    )
  }, [editing?.principal_id, members, tenantMembers])

  const editMember = (member: DirectoryMember) => {
    setEditing(member)
    setSelectedPrincipal(member.principal_id)
  }

  const cancelEdit = () => {
    setFormReset((value) => value + 1)
    setEditing(null)
    setSelectedPrincipal(firstAvailable())
  }

  const updateSelectedPrincipal = useCallback(
    (values: Record<string, unknown>) =>
      setSelectedPrincipal(String(values.principal_id ?? "")),
    []
  )

  const submit = async (data: Record<string, unknown>) => {
    const principalID = String(data.principal_id ?? "")
    if (!principalID) return
    const startsAt = toISOString(String(data.starts_at ?? ""))
    const endsAt = toISOString(String(data.ends_at ?? ""))
    if (startsAt && endsAt && Date.parse(endsAt) <= Date.parse(startsAt)) {
      onError(t("endAfterStart"))
      return
    }
    const saved = await onPut(principalID, {
      ...(startsAt ? { starts_at: startsAt } : {}),
      ...(endsAt ? { ends_at: endsAt } : {}),
    })
    if (saved) cancelEdit()
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="pr-8">
            {itemName} · {directoryLabel}
            {t("member")}
          </DialogTitle>
          <DialogDescription>
            {computed
              ? t("computedMembersDescription", { directory: directoryLabel })
              : t("assignMembersDescription", { directory: directoryLabel })}
          </DialogDescription>
        </DialogHeader>
        <div className="flex h-72 min-h-0 flex-col rounded-md border">
          <AutoTable<DirectoryMember>
            fill
            onRefresh={onRefresh}
            data={members}
            emptyContent={
              <>
                {t("noneYet")}
                {directoryLabel}
                {t("member")}
              </>
            }
            rowKey="principal_id"
            searchable
            columns={[
              {
                field: "display_name",
                name: t("member"),
                render: (_, member) => (
                  <>
                    <div className="font-medium">{member.display_name}</div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {member.principal_id}
                    </div>
                  </>
                ),
              },
              ...(!computed
                ? [
                    {
                      field: "starts_at",
                      name: scheduleLabel,
                      render: (_, member) => (
                        <span className="text-xs text-muted-foreground">
                          {membershipWindow(member, locale, t("unrestricted"))}
                        </span>
                      ),
                    } satisfies ColumnConfig<DirectoryMember>,
                  ]
                : []),
              {
                field: "status",
                name: t("status"),
                render: (_, member) =>
                  computed ? (
                    <Badge variant="secondary">{t("realtime")}</Badge>
                  ) : (
                    <Badge
                      variant={
                        membershipState(member) === "active"
                          ? "secondary"
                          : "outline"
                      }
                    >
                      {t(`membership_${membershipState(member)}`)}
                    </Badge>
                  ),
              },
              ...(!computed
                ? [
                    {
                      field: "actions",
                      type: "actions",
                      name: t("actions"),
                      render: (_, member) => (
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() => editMember(member)}
                            aria-label={t("editMemberSchedule", {
                              name: member.display_name,
                              schedule: scheduleLabel,
                            })}
                            title={t("editNamed", { name: scheduleLabel })}
                          >
                            <Pencil />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() => void onDelete(member)}
                            aria-label={t("removeNamed", {
                              name: member.display_name,
                            })}
                            title={t("removeMember")}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      ),
                    } satisfies ColumnConfig<DirectoryMember>,
                  ]
                : []),
            ]}
          />
        </div>
        {!computed && (
          <AutoForm
            key={editing?.principal_id ?? `assign:${formReset}`}
            id={formID}
            fields={{
              principal_id: {
                name: t("tenantMember"),
                type: "select",
                value: selectedPrincipal,
                disabled: Boolean(editing),
                config: {
                  id: inputPrefix,
                  options: availableMembers.map((member) => ({
                    value: member.subject,
                    label: member.display_name || member.subject,
                  })),
                },
              },
              starts_at: {
                name: t("startTime"),
                type: "text",
                value: toDateTimeLocal(editing?.starts_at),
                config: { id: `${inputPrefix}-start`, type: "datetime-local" },
              },
              ends_at: {
                name: t("endTime"),
                type: "text",
                value: toDateTimeLocal(editing?.ends_at),
                config: { id: `${inputPrefix}-end`, type: "datetime-local" },
              },
            }}
            onChange={updateSelectedPrincipal}
            onSubmit={submit}
          />
        )}
        {!computed && (
          <DialogFooter>
            {editing && (
              <Button variant="outline" disabled={busy} onClick={cancelEdit}>
                {t("cancelEdit")}
              </Button>
            )}
            <Button
              type="submit"
              form={formID}
              disabled={busy || !selectedPrincipal}
            >
              {busy
                ? t("saving")
                : editing
                  ? t("updatedAt")
                  : t("assignMember")}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}
