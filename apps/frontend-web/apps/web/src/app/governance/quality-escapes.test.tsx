import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import QualityEscapesPage from "./quality-escapes"
import { tenantSystemNavigation, entityNavigation, workspaceArea } from "../../lib/navigation"
function render(permissions: string[]) { return renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter initialEntries={["/admin/1/governance/escapes"]}><Routes><Route element={<Outlet context={{permissions:new Set(permissions),permissionsResolved:true}} />}><Route path="/admin/:tenantID/governance/escapes" element={<QualityEscapesPage />} /></Route></Routes></MemoryRouter></IntlAppProvider>) }
describe("quality escape audit boundary",()=>{
 it("keeps unauthorized readers out and offers only canonical permission-gated menus",()=>{expect(render([])).toContain(en.entranceAccessDenied);expect(render([])).not.toContain(en.qualityRecordLearning);expect(render(["audit_read"])).toContain(en.qualityLearningState);expect(tenantSystemNavigation(new Set(),false,false,false,false,false,false,["audit_read"]).filter(v=>v.path==="/governance/escapes")).toHaveLength(1);expect(entityNavigation(["audit_read"],"project").some(v=>v.path==="/escapes")).toBe(true);expect(workspaceArea("/governance/escapes")).toBe("governance")})
 it("has complete readable labels in all supported locales",()=>{const source=readFileSync(new URL("./quality-escapes.tsx",import.meta.url),"utf8");const keys=[...source.matchAll(/t\("(quality\w+)"\)/g)].map(v=>v[1]!);for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`,import.meta.url),"utf8"));for(const key of keys){expect(typeof messages[key]).toBe("string");expect(messages[key]).not.toContain("??")}}})
})
