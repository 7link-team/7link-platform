import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { EvalRunDetails, TemplateEvaluation } from "./evaluation"
import type { EvalRun } from "./eval-api"
import { softwareDefinition, type TemplateVersion } from "./workflow"

for (const locale of ["en-US", "zh-CN", "ms-MY"]) test(`evaluation Agent configuration and probe error counts in ${locale}`, async () => {
 const messages = await loadMessages(locale), t = messages.iam as Record<string,string>
 const row: EvalRun = {id:"91",template_version_id:"19",template_key:"software",state:"COMPLETED",snapshot:{template_version_id:"19",definition_sha256:"frozen-definition",target:{verifier_key:"visual",provider_ref:"provider:71:3:config-pin",machine_id:0,agent_id:"9007199254740993",role:"ACCEPTOR",model:"configured-model",configuration_sha256:"actual-config-fingerprint"},verifier:{},cases:[]},sha256:"actual-run-digest",results:[],cases_total:2,passed:0,failed:2,inconclusive:0,false_positives:1,false_negatives:1,agreement_ppm:0,limitations:"Only frozen labelled probes",created_by:"11",started_at:"2026-10-23T00:00:00Z",finished_at:"2026-10-23T00:01:00Z"}
 const wrap = (child: React.ReactNode) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}>{child}</IntlAppProvider>)
 const html=wrap(<EvalRunDetails row={row}/> )
 for (const text of ["ACCEPTOR","9007199254740993","configured-model","actual-config-fingerprint",t.templateEvalFalsePositives!,t.templateEvalFalseNegatives!]) expect(html).toContain(text)
 const version: TemplateVersion={id:"19",key:"software",state:"PUBLISHED",hash:"hash",version:1,definition:softwareDefinition()}
 const editor=wrap(<TemplateEvaluation tenantID="7" version={version} canRead canManage/> )
 for (const label of [t.templateEvalAgent!,t.templateEvalModel!,t.templateEvalAgentNotice!]) expect(editor).toContain(label)
 expect(wrap(<TemplateEvaluation tenantID="7" version={version} canRead canManage={false}/>)).not.toContain("<form")
})
