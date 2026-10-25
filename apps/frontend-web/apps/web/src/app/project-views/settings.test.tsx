import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ProjectSettingsDetails} from "./settings"
test("settings reuses complete contact/repository/member fields and explicit visibility in three locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=await loadMessages(locale)
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ProjectSettingsDetails project="99" settings={{name:"Project",description:null,target_date:null,version:4,contacts:[{intake_contact_id:"42",role:"PRIMARY",receives_deliveries:true,can_accept:true}],repositories:[{id:"70",name:"Source",remote_url:"https://example.test/repo.git",default_branch:"main",protected_branches:["main"],is_primary:true}],members:[{entity_id:"99",principal_id:"11",role_id:"81",effect:"allow",created_at:"2026-10-22T00:00:00Z",expires_at:null}],lead_agent_id:null,dispatch_policy:"MANUAL",portal_settings:{show_ai_label:false}}} onSaved={()=>{}}/></IntlAppProvider>)
 expect(html).toContain("https://example.test/repo.git");expect(html).toContain("42");expect(html).toContain("11 / 81 / allow");expect(html).toContain("add_members");expect(html).toContain("remove_members");expect(html).not.toContain("settingsContacts");expect(html).not.toContain("settingsMemberRules");expect(html).not.toContain("iam.")
 }
})
