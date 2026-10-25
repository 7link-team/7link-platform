import type {FieldRendererProps,FormConfig} from "@workspace/ui/components/auto-form"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {ProjectContactsField,ProjectRepositoriesField,ProjectMembersField} from "../projects/create-fields"
import {clientworkApi} from "../../lib/clientwork-api"
import {MutationForm} from "./client"

type Settings=Awaited<ReturnType<typeof clientworkApi.settings>>
type Portal={show_ai_label:boolean;show_work_item_titles:boolean;show_evidence_summary:boolean;show_release_notes:boolean;milestones_visible:boolean;contributor_display:"TEAM_ONLY"|"ROLES"}
const portalDefaults:Portal={show_ai_label:true,show_work_item_titles:true,show_evidence_summary:true,show_release_notes:true,milestones_visible:true,contributor_display:"TEAM_ONLY"}
export function ProjectVisibilityField({value,onChange,disabled}:FieldRendererProps){
 const t=useTranslations("iam"),v={...portalDefaults,...value as Partial<Portal>}
 return <fieldset disabled={disabled} className="space-y-3">{(["show_ai_label","show_work_item_titles","show_evidence_summary","show_release_notes","milestones_visible"] as const).map(key=><label key={key} className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={v[key]} onChange={e=>onChange({...v,[key]:e.target.checked})}/>{t(`settingsVisibility_${key}`)}</label>)}<label className="grid gap-2">{t("settingsContributorDisplay")}<select className="min-h-11 rounded-md border bg-background p-2" value={v.contributor_display} onChange={e=>onChange({...v,contributor_display:e.target.value})}><option value="TEAM_ONLY">{t("settingsTeamOnly")}</option><option value="ROLES">{t("settingsRoles")}</option></select></label></fieldset>
}
export function ProjectSettingsDetails({project,settings,onSaved}:{project:string;settings:Settings;onSaved:()=>void}){
 const t=useTranslations("iam")
 const groups:{title:string;fields:FormConfig;initial:Record<string,unknown>}[]=[
 {title:t("settingsContacts"),fields:{contacts:{name:t("settingsContacts"),type:"custom",component:ProjectContactsField}},initial:{contacts:settings.contacts??[]}},
 {title:t("settingsRepositories"),fields:{repositories:{name:t("settingsRepositories"),type:"custom",component:ProjectRepositoriesField}},initial:{repositories:settings.repositories??[]}},
 {title:t("settingsMembers"),fields:{add_members:{name:t("settingsAddMembers"),type:"custom",component:ProjectMembersField},remove_members:{name:t("settingsRemoveMembers"),type:"custom",component:ProjectMembersField}},initial:{add_members:[],remove_members:[]}},
 {title:t("settingsLeadDispatch"),fields:{lead_principal_id:{name:t("settingsLeadPrincipal"),type:"text"},dispatch_policy:{name:t("overviewDispatch"),type:"select",config:{options:[{value:"MANUAL",label:t("settingsManual")},{value:"AUTO",label:t("settingsAuto")}]}}},initial:{lead_principal_id:"",dispatch_policy:settings.dispatch_policy}},
 {title:t("settingsVisibility"),fields:{portal_settings:{name:t("settingsVisibility"),type:"custom",component:ProjectVisibilityField}},initial:{portal_settings:settings.portal_settings}}
 ]
 return <div className="grid gap-6">{groups.map(g=><section key={g.title} className="grid gap-3 border-t pt-4"><h2 className="font-medium">{g.title}</h2>{g.title===t("settingsMembers")&&<><p className="text-sm text-muted-foreground">{t("settingsMemberRules")}</p><ul>{settings.members?.map(m=><li key={`${m.principal_id}:${m.role_id}`}>{m.principal_id} / {m.role_id} / {m.effect}</li>)}</ul></>}{g.title===t("settingsLeadDispatch")&&<p className="text-sm">{t("projectLeadAgent")}: {settings.lead_agent_id??t("settingsNoLead")}</p>}<MutationForm fields={g.fields} initialData={g.initial} label={t("clientSave")} submit={async(values,key)=>{const body={...values};if(body.lead_principal_id==="")delete body.lead_principal_id;await clientworkApi.patchSettings(project,body,settings.version,key);onSaved()}}/></section>)}</div>
}
