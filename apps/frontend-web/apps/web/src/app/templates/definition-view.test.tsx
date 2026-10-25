import { expect,test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { MetadataValue,TemplateDefinitionView } from "./definition-view"
import { softwareDefinition,type TemplateVersion } from "./workflow"

for(const locale of ["en-US","zh-CN","ms-MY"])test(`definition metadata stays separate from runtime facts in ${locale}`,async()=>{
 const messages=await loadMessages(locale), strings=messages.iam as Record<string,string>
 const version:TemplateVersion={id:"11",key:"software",origin:"TEAM",state:"PUBLISHED",hash:"hash",version:2,definition:softwareDefinition()}
 version.definition.metadata={team_modes:["HUMAN_ONLY"],verifiers:[{verifier_key:"actual-declaration",limitations:"manual"}],rubric:{dimensions:[]}}
 const before=JSON.stringify(version)
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><TemplateDefinitionView version={version}/></IntlAppProvider>)
 for(const key of ["templateOverview","templateArtifacts","templateVerifiers","templateRubric","templateQualityGates","templateDefinitionNotice"])expect(html).toContain(strings[key]!)
 expect(html).toContain("HUMAN_ONLY");expect(html).toContain("actual-declaration");expect(html).toContain('aria-pressed="true"')
 expect(JSON.stringify(version)).toBe(before)
 expect(html).not.toContain("calibration_state")
})

test("structured metadata preserves dimensions, tool requirements, empty and missing distinctions and escapes extension content",()=>{
 const value={dimensions:[{name:"Quality",weight:"0.75",hard_gate:true}],required_tools:["ffprobe"],limitations:"<script>evil</script>",empty:[],missing:null,disabled:false}
 const html=renderToStaticMarkup(<MetadataValue value={value} label={key=>key}/>)
 expect(html).toContain("<dl");expect(html).toContain("<dt");expect(html).toContain("0.75");expect(html).toContain("ffprobe")
 expect(html).toContain("templateMetadataEmpty");expect(html).toContain("templateMetadataAbsent");expect(html).toContain("templateValueFalse");expect(html).toContain("templateValueTrue")
 expect(html).not.toContain("<script>");expect(html).toContain("&lt;script&gt;")
})
