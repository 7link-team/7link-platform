import {createRoot} from "react-dom/client"
import {useState} from "react"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../src/lib/i18n"
import {WorkdirList} from "../../src/app/machines/workdirs"
import {ProvenanceFacts} from "../../src/app/executions/provenance"
import type {Workdir} from "../../src/app/machines/workdirs-api"
import "../../src/index.css"
const query=new URLSearchParams(location.search)
const locale=query.get("locale")??"zh-CN"
const calls: unknown[]=[];Object.assign(window,{fixtureCalls:calls})
let stored:Workdir={id:"9007199254740993",project_id:"21",repository_id:"31",path:"D:/repo",version:1}
let fail=true
export default function Fixture(){
 const [rows,setRows]=useState([stored])
 return <main style={{padding:16,minWidth:0}}><WorkdirList machineID="11" rows={rows} manage={!query.has("readonly")} probe={!query.has("readonly")} online={true} reload={async()=>setRows([{...stored}])} api={{update:async(machine,row,body,key)=>{
 calls.push({machine,row,body,key});if(fail){fail=false;throw new Error("Fixture temporary 503; retry same intent")}
 if(body.path.includes("blocked"))throw new Error("Fixture active execution conflict 409")
 stored={...row,path:body.path,version:row.version+1,archived_at:body.archived?"2026-10-22T12:00:00Z":undefined};return stored
 },probe:async(machine,id)=>{calls.push({probe:id,machine});return {state:"QUEUED"}}}}/>
 <ProvenanceFacts page={{items:[{id:"11",execution_id:"21",producer_id:"9007199254740995",machine_id:"31",kind:"FILE",title:"Partial source fixture",content_hash:"a".repeat(64),created_at:"2026-10-22T00:00:00Z",plan_hash:"b".repeat(64),base_commit:"c".repeat(40),output_state:"PARTIAL",comparison:"UNKNOWN",artifact:{binding_id:"41",space_id:"42",file_id:"43",version_id:"44",file_version:3,coverage:"PARTIAL",note:"incomplete output",sha256:"a".repeat(64)}}],has_more:false,comparison_truncated:true,verifiers_truncated:false,limitations:[],verifiers:[]}}/>
 </main>
}
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><Fixture/></IntlAppProvider>)
