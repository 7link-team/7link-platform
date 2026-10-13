import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { WorkspaceTenantRows } from "./workspace-switcher"

const tenants = [
  { id: "1", slug: "first", name: "First" },
  { id: "2", slug: "second", name: "Second" },
]

function rows(groupEntitiesUnderTenant: boolean) {
  const html = renderToStaticMarkup(
    <WorkspaceTenantRows
      tenants={tenants}
      tenantID="1"
      groupEntitiesUnderTenant={groupEntitiesUnderTenant}
      onTenant={() => {}}
    />
  )
  const row = (name: string) =>
    [...html.matchAll(/<(h3|button)\b.*?<\/\1>/g)].find((match) =>
      match[0].includes(`>${name}<`)
    )?.[1]
  return { first: row("First"), second: row("Second") }
}

describe("WorkspaceTenantRows", () => {
  // Not selectable is not the same as not shown: somebody still has to see which
  // tenant their entities belong to. The row stays and stops being a button.
  // The standing behind that is the current tenant's, so no other tenant in the
  // list loses its way in.
  it("makes only the current tenant a heading when its entities are the destinations", () => {
    expect(rows(true)).toEqual({ first: "h3", second: "button" })
  })
  it("offers every tenant as a destination otherwise", () => {
    expect(rows(false)).toEqual({ first: "button", second: "button" })
  })
})
