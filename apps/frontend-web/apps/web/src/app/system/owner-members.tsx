import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams } from "react-router"
import { ArrowLeft, Plus, UserCog, Loader2 } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from "@workspace/ui/components/table"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { SystemMembershipDialog } from "../../components/system-membership-dialog"
import {
  iamApi,
  type Member,
  type Principal,
  type Department,
  type Role,
  type SystemOwnerScope,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export function PlatformOwnerMembersPage() {
  return <OwnerMembersPage kind="platform" />
}
export function TenantOwnerMembersPage() {
  return <OwnerMembersPage kind="tenant" />
}

function OwnerMembersPage({ kind }: { kind: SystemOwnerScope["kind"] }) {
  const { ownerID = "" } = useParams()
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [name, setName] = useState("")
  const scope = useMemo(
    () => ({ kind, id: ownerID, name }),
    [kind, ownerID, name]
  )
  const [members, setMembers] = useState<Member[]>([])
  const [departments, setDepartments] = useState<Department[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [search, setSearch] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [account, setAccount] = useState<Principal | null>(null)
  const generation = useRef(0)
  const invalidate = useCallback(() => {
    generation.current++
  }, [])
  const refresh = useCallback(async () => {
    const current = ++generation.current
    setError("")
    try {
      const [nextMembers, nextDepartments, nextRoles] = await Promise.all([
        iamApi.systemOwnerMembers(scope),
        iamApi.systemOwnerDepartments(scope),
        iamApi.systemOwnerRoles(scope),
      ])
      if (current !== generation.current) return
      setMembers(nextMembers)
      setDepartments(nextDepartments)
      setRoles(nextRoles)
    } catch (cause) {
      if (current === generation.current) setError((cause as Error).message)
    }
  }, [scope])
  useEffect(() => {
    let active = true
    void iamApi.systemAccountOwners(kind).then(
      (owners) => {
        if (active)
          setName(owners.find((owner) => owner.id === ownerID)?.name ?? ownerID)
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [kind, ownerID])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(async () => {
      if (active) {
        setLoading(true)
        setOpen(false)
        await refresh()
        if (active) setLoading(false)
      }
    })
    return () => {
      active = false
      invalidate()
    }
  }, [refresh, invalidate])
  return (
    <>
      <PageHeader
        title={`${t("manageMemberships")} · ${name || ownerID}`}
        action={
          allowed("platform_administer") ? (
            <Button
              onClick={() => {
                setAccount(null)
                setOpen(true)
              }}
            >
              <Plus />
              {t("create")}
            </Button>
          ) : undefined
        }
      />
      <Button asChild variant="ghost" className="mb-4">
        <Link to={`/system/accounts/${kind}`}>
          <ArrowLeft />
          {t(kind === "platform" ? "platformAccounts" : "tenantAccounts")}
        </Link>
      </Button>
      {error && <Alert>{error}</Alert>}
      <Input
        aria-label={t("searchPrincipals")}
        value={search}
        onChange={(event) => setSearch(event.target.value)}
        className="mb-4 max-w-sm"
      />
      {loading ? (
        <Loader2 className="size-5 animate-spin" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("displayName")}</TableHead>
              <TableHead>{t("email")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("department")}</TableHead>
              <TableHead>{t("assignedRoles")}</TableHead>
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {members
              .filter((member) =>
                `${member.display_name ?? ""} ${member.email ?? ""}`
                  .toLowerCase()
                  .includes(search.toLowerCase())
              )
              .map((member) => (
                <TableRow
                  key={member.subject}
                  data-owner-member-id={member.subject}
                >
                  <TableCell>{member.display_name || member.subject}</TableCell>
                  <TableCell>{member.email}</TableCell>
                  <TableCell>{t(member.status)}</TableCell>
                  <TableCell>
                    {departments.find(
                      (department) => department.id === member.department_id
                    )?.name ?? "-"}
                  </TableCell>
                  <TableCell>
                    {(member.role_ids ?? [])
                      .map(
                        (id) => roles.find((role) => role.id === id)?.name ?? id
                      )
                      .join(", ") || "-"}
                  </TableCell>
                  <TableCell>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={t("manageMembershipsNamed", {
                        name: member.display_name || member.subject,
                      })}
                      onClick={() => {
                        setAccount({
                          id: member.subject,
                          display_name: member.display_name,
                          email: member.email,
                          status: member.status,
                          activation_required: false,
                          created_at: member.created_at,
                          updated_at: member.updated_at,
                        })
                        setOpen(true)
                      }}
                    >
                      <UserCog />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      )}
      <SystemMembershipDialog
        key={`${kind}:${ownerID}:${account?.id ?? "new"}`}
        scope={scope}
        account={account}
        open={open}
        onClose={() => setOpen(false)}
        onChanged={() => void refresh()}
      />
    </>
  )
}
