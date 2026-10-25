import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {CalibrationStatus,TemplateCalibration} from "./calibration"
import type {Calibration} from "./calibration-api"
import {softwareDefinition,type TemplateVersion} from "./workflow"
for(const locale of ["en-US","zh-CN","ms-MY"])test(`calibration renders actual unknown, drift, frozen evidence and permission boundary in ${locale}`,async()=>{
 const messages=await loadMessages(locale),t=messages.iam as Record<string,string>
 const row:Calibration={id:"9007199254740993",template_version_id:"19",template_key:"software",verifier_key:"visual",provider_ref:"real-evaluator/version",method:"AGREEMENT",machine_id:null,state:"UNCALIBRATED",run_id:"21",run_state:"UNAVAILABLE",target_ppm:850000,sample_size:20,next_review_at:"2026-11-21T00:00:00Z",version:2,limitations:"Actual source evaluator not connected",results:[]}
 const render=()=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><CalibrationStatus row={row}/></IntlAppProvider>)
 const unavailable=render();for(const value of [t.templateCalibrationUnknown,t.templateCalibrationUnavailable,t.templateCalibrationAdvisory,"85%","Actual source evaluator not connected","2026-11-21T00:00:00Z"])expect(unavailable).toContain(value!)
 expect(unavailable).not.toContain('0%')
 row.state="DRIFTED";row.run_state="COMPLETED";row.agreement_ppm=600000;row.drift_checked_at="2026-10-22T00:00:00Z";row.results=[{sample_id:"91",verdict:"FAIL",evidence_refs:["immutable:evidence:7"]}]
 const drifted=render();for(const value of [t.templateCalibrationState_DRIFTED,"60%","immutable:evidence:7","2026-10-22T00:00:00Z"])expect(drifted).toContain(value!)
 const version:TemplateVersion={id:"19",key:"software",state:"PUBLISHED",hash:"hash",version:1,definition:softwareDefinition()};version.definition.metadata.verifiers=[{verifier_key:"visual",kind:"MODEL_SCORED"}]
 const panel=(read:boolean,manage:boolean)=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><TemplateCalibration tenantID="7" version={version} canRead={read} canManage={manage}/></IntlAppProvider>)
 expect(panel(false,false)).toContain(t.templateCalibrationDenied!);expect(panel(false,false)).not.toContain("<form")
 expect(panel(true,false)).not.toContain("<form");const manage=panel(true,true);expect(manage).toContain(t.templateCalibrationLabelsNotice!);expect(manage).toContain(t.templateCalibrationStartNotice!);expect(manage).toContain("AGREEMENT");expect(manage.match(/type="submit"/g)).toHaveLength(2);expect(manage).toContain(`aria-label="${t.templateCalibrationLabels}"`)
 for(const[key,value]of Object.entries(t).filter(([key])=>key.startsWith("templateCalibration"))){expect(value).not.toBe(key);expect(value).not.toMatch(/^\?+$/)}
})
