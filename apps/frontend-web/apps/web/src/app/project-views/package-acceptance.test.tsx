import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { packageFeedbackCategories, packageItemDecision, packageReworkMatches, type ClientRecords } from "../../lib/clientwork-api"
import type { components } from "../../lib/api-schema"
import { PackageAcceptance } from "./package-acceptance"

const first = "a".repeat(64), second = "b".repeat(64), hash = "c".repeat(64)
const row: ClientRecords["packages"] = {
  id: "9007199254740993", state: "PUBLISHED", version: 4, archived_at: null, content_hash: hash,
  content: { title: "Exact manifest", release_notes: "Frozen release", artifacts: [] },
  items: [first, second].map((item_id, index) => ({ item_id, artifact: { name: `File ${index + 1}`, version_id: String(index + 8), sha256: "d".repeat(64), coverage: "verified", verification_overrides: null } })),
}

it("offline controls require internal delivery send authority and immutable receipts expose recorder and proof in every locale", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale), labels = messages.iam as Record<string,string>
    for (const external of [false,true]) for (const permitted of [false,true]) {
      const router = createMemoryRouter([{element:<Outlet context={{permissions:new Set(permitted?["delivery_send"]:[]),permissionsResolved:true}}/>,children:[{path:"/",element:<PackageAcceptance project="3" row={row} access={{external,principal_id:external?"4":"2"}} mutate={async()=>{}}/>}]}])
      const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
      expect(html.includes(labels.packageRecordOffline!)).toBe(permitted&&!external)
      expect(html.includes(labels.packageOfflineFileID!)).toBe(permitted&&!external)
      router.dispose()
    }
    const recorded={...row,state:"ACCEPTED",decision:{confirmed:true,signer_name:"Client",content_hash:hash,decision:"ACCEPT" as const,reason:"Signed offline",items:[{item_id:first,result:"ACCEPTED" as const}]},signature:{source:"RECORDED" as const,recorded_by:"22",signer_name:"Client",receipt_sha256:hash,receipt_json:"{}",evidence:{id:"91",file_id:"92",version_id:"93",name:"signed-document.pdf",mime:"application/pdf",sha256:hash,size:4}}}
    const router=createMemoryRouter([{path:"/",element:<PackageAcceptance project="3" row={recorded} access={{external:true,principal_id:"4"}} mutate={async()=>{}}/>}])
    const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
    expect(html).toContain(labels.packageOfflineReceipt!)
    expect(html).toContain("signed-document.pdf")
    expect(html).toContain("22")
    expect(html).toContain(labels.packageOfflineEvidence!)
    expect(html).not.toContain(labels.packageRecordOffline!)
    router.dispose()
  }
})

it("builds every exact manifest item in server order and validates Unicode rejection feedback", () => {
  const values = { signer_name: "Client signer", confirmed: true, reason: "Checked exact content", [`result_${first}`]: "ACCEPTED", [`result_${second}`]: "REJECTED", [`category_${second}`]: "FUNCTIONAL_DEFECT", [`comment_${second}`]: "😀".repeat(10) }
  const body = packageItemDecision(row, values, "Invalid decisions")
  expect(body).toEqual({ content_hash: hash, signer_name: values.signer_name, confirmed: true, reason: values.reason, items: [{ item_id: first, result: "ACCEPTED" }, { item_id: second, result: "REJECTED", feedback_category: "FUNCTIONAL_DEFECT", comment: "😀".repeat(10) }] })
  expect(body.decision).toBeUndefined() // Aggregate belongs to the canonical server.
  for (const patch of [
    { signer_name: " " }, { signer_name: "x".repeat(201) }, { confirmed: false }, { confirmed: undefined }, { confirmed: "true" },
    { [`result_${first}`]: undefined }, { [`result_${second}`]: "PENDING" }, { [`category_${second}`]: "MISC" },
    { [`comment_${second}`]: "😀".repeat(9) }, { [`comment_${second}`]: " ".repeat(20) },
    { [`comment_${second}`]: "😀".repeat(2001) }, { reason: " " }, { reason: "x".repeat(2001) },
  ]) expect(() => packageItemDecision(row, { ...values, ...patch }, "Invalid decisions")).toThrow("Invalid decisions")
  for (const category of packageFeedbackCategories) expect(packageItemDecision(row, { ...values, [`category_${second}`]: category }, "Invalid").items?.[1]?.feedback_category).toBe(category)
  expect(() => packageItemDecision({ ...row, items: [] }, values, "Invalid")).toThrow()
  expect(() => packageItemDecision({ ...row, items: [row.items![0]!, row.items![0]!] }, values, "Invalid")).toThrow()
  expect(() => packageItemDecision({ ...row, content_hash: "different" }, values, "Invalid")).toThrow()
  expect(packageItemDecision(row, { ...values, [`result_${second}`]: "ACCEPTED" }, "Invalid").items?.[1]).toEqual({ item_id: second, result: "ACCEPTED" })
})

it("rejects rework sources associated with another exact package, project, receipt or content", () => {
  const source: components["schemas"]["ReworkSourceView"] = { id: "9007199254740995", project_id: "3", package_id: row.id, receipt_id: "9007199254740997", content_hash: hash, version: 1, state: "TRIAGED", rejection: { package_id: row.id, content_hash: hash, receipt_id: "9007199254740997", recipient_id: "4", items: [] } }
  expect(packageReworkMatches(source, "3", { ...row, rework_intake_id: source.id! })).toBe(true)
  for (const patch of [{ project_id: "5" }, { package_id: "9007199254740992" }, { content_hash: "d".repeat(64) }, { receipt_id: "9" }, { id: null }]) expect(packageReworkMatches({ ...source, ...patch }, "3", row)).toBe(false)
  expect(packageReworkMatches(source, "3", { ...row, rework_intake_id: "9007199254740999" })).toBe(false)
})

it("renders immutable outcomes and gates private rework controls on INTERNAL current scope authority", async () => {
  const rejected = { ...row, state: "PARTIALLY_ACCEPTED", rework_unavailable: true, rework_unavailable_reason: "REWORK_SOURCE_UNAVAILABLE", decision: { signer_name: "Client signer", confirmed: true, content_hash: hash, reason: "Exact feedback", decision: "PARTIALLY_ACCEPTED" as const, items: [{ item_id: second, result: "REJECTED" as const, feedback_category: "SECURITY" as const, comment: "Private output escaped correctly <script>" }] } }
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale), labels = messages.iam as Record<string, string>
    for (const [access, permissions, visible] of [[null, ["scope_change"], false], [{ external: true, principal_id: "4" }, ["scope_change"], false], [{ external: false, principal_id: "2" }, [], false], [{ external: false, principal_id: "2" }, ["scope_change"], true]] as const) {
      const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(permissions), permissionsResolved: true }}/>, children: [{ path: "/", element: <PackageAcceptance project="3" row={rejected} access={access} mutate={async () => {}}/> }] }])
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
      expect(html).toContain(second)
      expect(html).toContain(labels.packageItem_REJECTED!)
      expect(html).toContain(labels.packageFeedback_SECURITY!)
      expect(html).toContain("&lt;script&gt;")
      expect(html.includes(labels.packageReadRework!)).toBe(visible)
      expect(html.includes(labels.packageRetryRework!)).toBe(visible)
      expect(html).not.toContain("can_accept")
      expect(html).not.toContain("packageAcceptance")
    }
    for (const key of Object.keys(labels).filter(key => key.startsWith("package"))) expect(labels[key]).not.toBe(key)
  }
})
