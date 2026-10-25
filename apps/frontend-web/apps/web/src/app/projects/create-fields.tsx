import {useId} from "react"
import type {FieldRendererProps} from "@workspace/ui/components/auto-form"
import {Button} from "@workspace/ui/components/button"
import {Input} from "@workspace/ui/components/input"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import type {ProjectCreateInput} from "../../lib/iam-api"

const selectClass = "h-9 w-full rounded-md border bg-background px-3 text-sm"

export function ProjectClientField({value, onChange, disabled}: FieldRendererProps) {
  const t = useTranslations("iam")
  const id = useId()
  const client = value as ProjectCreateInput["client"]
  const mode = client?.entity_id !== undefined ? "existing" : client?.new_name !== undefined ? "new" : "none"
  return <div className="space-y-2"><select aria-label={t("projectClientChoice")} className={selectClass} disabled={disabled} value={mode} onChange={event => onChange(event.target.value === "existing" ? {entity_id: ""} : event.target.value === "new" ? {new_name: ""} : undefined)}><option value="none">{t("projectClientNone")}</option><option value="existing">{t("projectClientExisting")}</option><option value="new">{t("projectClientNew")}</option></select>{mode !== "none" && <label htmlFor={id} className="block space-y-1 text-sm"><span>{t(mode === "existing" ? "projectClientEntity" : "projectClientName")}</span><Input id={id} disabled={disabled} value={mode === "existing" ? client?.entity_id ?? "" : client?.new_name ?? ""} maxLength={mode === "existing" ? 19 : 200} onChange={event => onChange(mode === "existing" ? {entity_id: event.target.value} : {new_name: event.target.value})}/></label>}</div>
}

export function ProjectContactsField({value, onChange, disabled}: FieldRendererProps) {
  const t = useTranslations("iam")
  const id = useId()
  const rows = (Array.isArray(value) ? value : []) as NonNullable<ProjectCreateInput["contacts"]>
  const update = (index: number, patch: Partial<(typeof rows)[number]>) => onChange(rows.map((row, i) => i === index ? {...row, ...patch} : row))
  return <div className="space-y-3">{rows.map((row, index) => <fieldset key={index} disabled={disabled} className="space-y-2 rounded-md border p-3"><legend className="px-1 text-sm">{t("projectContact")} {index+1}</legend><label htmlFor={`${id}-${index}`} className="block space-y-1 text-sm"><span>{t("projectContactID")}</span><Input id={`${id}-${index}`} maxLength={19} value={row.intake_contact_id} onChange={event => update(index, {intake_contact_id: event.target.value})}/></label><select className={selectClass} aria-label={t("projectContactRole")} value={row.role ?? "CC"} onChange={event => update(index, {role: event.target.value as "PRIMARY"|"CC"})}><option value="PRIMARY">{t("projectContactPrimary")}</option><option value="CC">{t("projectContactCC")}</option></select><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={row.receives_deliveries ?? true} onChange={event => update(index, {receives_deliveries: event.target.checked})}/>{t("projectContactReceives")}</label><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={row.can_accept ?? true} onChange={event => update(index, {can_accept: event.target.checked})}/>{t("projectContactAccepts")}</label><Button type="button" variant="outline" onClick={() => onChange(rows.filter((_, i) => i !== index))}>{t("projectCreateRemove")}</Button></fieldset>)}<Button type="button" variant="outline" disabled={disabled || rows.length >= 20} onClick={() => onChange([...rows, {intake_contact_id: "", role: rows.length ? "CC" : "PRIMARY", receives_deliveries: true, can_accept: true}])}>{t("projectContactAdd")}</Button></div>
}

export function ProjectRepositoriesField({value, onChange, disabled}: FieldRendererProps) {
  const t = useTranslations("iam")
  const id = useId()
  const rows = (Array.isArray(value) ? value : []) as NonNullable<ProjectCreateInput["repositories"]>
  const update = (index: number, patch: Partial<(typeof rows)[number]>) => onChange(rows.map((row, i) => i === index ? {...row, ...patch} : row))
  return <div className="space-y-3">{rows.map((row, index) => <fieldset key={index} disabled={disabled} className="space-y-2 rounded-md border p-3"><legend className="px-1 text-sm">{t("projectRepository")} {index+1}</legend>{(["name", "remote_url", "default_branch"] as const).map(field => <label key={field} htmlFor={`${id}-${index}-${field}`} className="block space-y-1 text-sm"><span>{t(field === "name" ? "projectRepositoryName" : field === "remote_url" ? "projectRepositoryURL" : "projectRepositoryBranch")}</span><Input id={`${id}-${index}-${field}`} value={row[field] ?? ""} maxLength={field === "remote_url" ? 2000 : field === "name" ? 100 : 200} onChange={event => update(index, {[field]: event.target.value})}/></label>)}<label htmlFor={`${id}-${index}-protected`} className="block space-y-1 text-sm"><span>{t("projectRepositoryProtected")}</span><Input id={`${id}-${index}-protected`} value={row.protected_branches?.join(", ") ?? ""} onBlur={() => update(index, {protected_branches: row.protected_branches?.filter(Boolean) ?? []})} onChange={event => update(index, {protected_branches: event.target.value.split(",").map(value => value.trim())})}/></label><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={row.is_primary ?? false} onChange={event => update(index, {is_primary: event.target.checked})}/>{t("projectRepositoryPrimary")}</label><Button type="button" variant="outline" onClick={() => onChange(rows.filter((_, i) => i !== index))}>{t("projectCreateRemove")}</Button></fieldset>)}<Button type="button" variant="outline" disabled={disabled || rows.length >= 10} onClick={() => onChange([...rows, {name: "", remote_url: "", default_branch: "main", protected_branches: ["main"], is_primary: rows.length === 0}])}>{t("projectRepositoryAdd")}</Button></div>
}

export function ProjectMembersField({value, onChange, disabled}: FieldRendererProps) {
  const t = useTranslations("iam")
  const id = useId()
  const rows = (Array.isArray(value) ? value : []) as NonNullable<ProjectCreateInput["members"]>
  const update = (index: number, patch: Partial<(typeof rows)[number]>) => onChange(rows.map((row, i) => i === index ? {...row, ...patch} : row))
  return <div className="space-y-3"><p className="text-sm text-muted-foreground">{t("projectMemberHint")}</p>{rows.map((row, index) => <fieldset key={index} disabled={disabled} className="space-y-2 rounded-md border p-3"><legend className="px-1 text-sm">{t("projectMember")} {index+1}</legend><label htmlFor={`${id}-${index}-principal`} className="block space-y-1 text-sm"><span>{t("principalID")}</span><Input id={`${id}-${index}-principal`} maxLength={19} value={row.principal_id} onChange={event => update(index, {principal_id: event.target.value})}/></label><label htmlFor={`${id}-${index}-role`} className="block space-y-1 text-sm"><span>{t("projectMemberRole")}</span><Input id={`${id}-${index}-role`} value={row.role_key} onChange={event => update(index, {role_key: event.target.value})}/></label><Button type="button" variant="outline" onClick={() => onChange(rows.filter((_, i) => i !== index))}>{t("projectCreateRemove")}</Button></fieldset>)}<Button type="button" variant="outline" disabled={disabled || rows.length >= 50} onClick={() => onChange([...rows, {principal_id: "", role_key: "engineer"}])}>{t("projectMemberAdd")}</Button></div>
}
