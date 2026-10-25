import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import PlanReviewPage from "./plan-review"
import { entityNavigation } from "../../lib/navigation"
function render(permissions: string[]) {return renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter initialEntries={["/admin/1/entities/99/plan-review"]}><Routes><Route element={<Outlet context={{permissions:new Set(permissions),permissionsResolved:true}} />}><Route path="/admin/:tenantID/entities/:entityID/plan-review" element={<PlanReviewPage />} /></Route></Routes></MemoryRouter></IntlAppProvider>)}
describe("readonly plan review",()=>{
 it("separates work readers from human proposal authors and exposes explicit provenance",()=>{expect(render([])).toContain(en.entranceAccessDenied);expect(render(["work_item_read"])).not.toContain(en.planReviewPropose);expect(render(["work_item_read","work_item_write"])).toContain(en.planReviewPropose);expect(render(["work_item_read"])).toContain(en.planReviewHint);expect(entityNavigation(["work_item_read"],"project").filter(v=>v.path==="/plan-review")).toHaveLength(1)})
 it("translates the review and criterion form in all locales",()=>{const source=readFileSync(new URL("./plan-review.tsx",import.meta.url),"utf8");const keys=[...source.matchAll(/t\("(planReview\w+)"\)/g)].map(v=>v[1]!);for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`,import.meta.url),"utf8"));for(const key of keys){expect(typeof messages[key]).toBe("string");expect(messages[key]).not.toContain("??")}}})
})
