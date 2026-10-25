import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { PackageBundle } from "./package-bundle"
import { loadMessages } from "../../lib/i18n"

it("exposes an explicit bundle build action to editors and no build mutation to clients in every locale",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"] as const){
  const messages=await loadMessages(locale),labels=messages.iam as Record<string,string>
  const render=(editable:boolean)=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><PackageBundle project="3" id="70" editable={editable} onChanged={()=>{}} mutate={async()=>{}}/></IntlAppProvider>)
  const editor=render(true),client=render(false)
  expect(editor).toContain('role="status"')
  expect(editor).toContain('aria-label=')
  expect(editor).toContain('<button')
  expect(editor).toContain(labels.packageBundleTitle!)
  expect(editor).toContain(labels.packageBundleBuild!)
  expect(client).toContain(labels.packageBundleTitle!)
  expect(client).not.toContain('<button')
  expect(client).not.toContain(labels.packageBundleBuild!)
  expect(client).not.toContain(labels.packageBundleDownload!)
 }
})
