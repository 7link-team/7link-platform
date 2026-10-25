import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ReleaseNoteHistory} from "./release-notes"
import type {ReleaseNoteDraft} from "./release-notes-api"

test("draft history shows real Agent and frozen fingerprint in all locales without implicitly adopting",async()=>{
 const row={id:"7",drafter_principal_id:"8",state:"DRAFT",content:"Actual Agent text",note:"Summarize changes",source_fingerprint:"a".repeat(64),created_at:"2026-10-22T00:00:00Z"} as ReleaseNoteDraft
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const messages=await loadMessages(locale)
  for(const allowed of [false,true]){
   const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ReleaseNoteHistory rows={[row]} onAdopt={allowed?async()=>{}:undefined}/></IntlAppProvider>)
   expect(html).toContain(row.source_fingerprint);expect(html).toContain("Actual Agent text");expect(html).toContain("Summarize changes")
   expect(html.includes("<form")).toBe(allowed);expect(html).not.toContain("releaseNote")
  }
 }
})
