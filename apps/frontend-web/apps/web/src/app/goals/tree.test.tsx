import {expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {GoalTreeView} from "./tree"
import {goalTreeVisible,krProgress,type GoalFilters} from "./tree-model"
import type {GoalTreeRow,GoalInput} from "./api"
const filters:GoalFilters={project:"",owner:"",state:"",period:"",start:"",origin:""}
const row=(id:string,parent?:string):GoalTreeRow=>({id,kind:"goal",project_id:parent?"3":"0",principal_id:parent?"9":"2",period_kind:parent?"MONTH":"YEAR",period_start:"2026-10-01",state:parent?"AT_RISK":"ACTIVE",origin:parent?"AGENT_SPAWNED":"HUMAN",elapsed:0.4,version:1,content_hash:"hash",progress:0.5,content:{level:parent?"MEMBER":"TEAM",title:`Goal ${id}`,...(parent?{parent_goal_id:parent,parent_kr:"k"}:{}),key_results:[{key:"k",title:"Quality",source:"MANUAL",baseline:10,current:5,target:0}]}})
it("retains authorized ancestors for every exact filter",()=>{
 const rows=[row("9007199254740993"),row("9007199254740995","9007199254740993"),row("77")]
 for(const change of [{project:"3"},{owner:"9"},{state:"AT_RISK"},{period:"MONTH"},{origin:"AGENT_SPAWNED"}])expect([...goalTreeVisible(rows,{...filters,...change})]).toEqual(["9007199254740995","9007199254740993"])
 expect(goalTreeVisible(rows,{...filters,start:"2026-11-01"}).size).toBe(0)
 expect([...goalTreeVisible([row("5","hidden")],filters)]).toEqual(["5"])
 expect(goalTreeVisible([row("5","6"),row("6","5")],filters).size).toBe(2)
})
it("supports decreasing and zero-span KR targets without overflow",()=>{
 const kr=(row("1").content as GoalInput).key_results[0]!
 expect(krProgress(kr)).toBe(0.5);expect(krProgress({...kr,current:-5})).toBe(1);expect(krProgress({...kr,current:15})).toBe(0);expect(krProgress({...kr,target:10,current:10})).toBe(1)
})
for(const locale of ["en-US","zh-CN","ms-MY"] as const)it(`renders expandable hierarchy and workday bars in ${locale}`,()=>{
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><GoalTreeView rows={[row("1"),row("2","1")]} filters={{...filters,owner:"9"}} open={()=>{}}/></IntlAppProvider>)
 expect(html.match(/<details/g)?.length).toBe(2);expect(html).toContain("40%");expect(html).toContain("50%");expect(html).toContain("Goal 1");expect(html).toContain("Goal 2");expect(html).toContain('aria-label=')
})
