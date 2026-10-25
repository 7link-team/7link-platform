import { PlanningPage } from "../goals/page"
import { PlanningSettingsButton } from "../plans/settings"
import {useSearchParams} from "react-router"
import {useLocale} from "@workspace/ui/i18n/intl-provider"
import {Button} from "@workspace/ui/components/button"
import {ReportBoard} from "./board"
import {reportLabels} from "./labels"
export default function ReportsPage() {const [search,setSearch]=useSearchParams();const l=reportLabels(useLocale());const view=search.get("record_id")?"list":search.get("view")??"list";return <div className="flex h-full min-h-0 flex-col"><div className="mb-3 flex flex-wrap justify-between gap-3"><nav aria-label={l.list} className="flex gap-2">{(["list","rollup","calendar"] as const).map(tab=><Button key={tab} variant={view===tab?"default":"outline"} onClick={()=>setSearch(p=>{const n=new URLSearchParams(p);n.delete("record_id");n.set("view",tab);return n})}>{l[tab]}</Button>)}</nav><PlanningSettingsButton/></div><div className="min-h-0 flex-1">{view==="rollup"||view==="calendar"?<ReportBoard view={view}/>:<PlanningPage kind="reports"/>}</div></div>}
