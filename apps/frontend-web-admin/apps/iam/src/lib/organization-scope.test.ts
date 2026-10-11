import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { organizationRouteURL, tenantIDFromPath, entityIDFromPath } from "./organization-scope"

it("selects operating collection routes while preserving owned and tenant boundaries", () => {
  const result = Bun.spawnSync(
    [
      process.execPath,
      "--eval",
      `
    import assert from "node:assert/strict";
    import {createIamApi} from "./iam-api.ts";
    import {organizationRouteURL,routeTenantID,operatingPlatformID} from "./organization-scope.ts";
    globalThis.window = Object.assign(new EventTarget(), {location:{pathname:"/platform/42/roles"}});
    const calls=[];
    globalThis.fetch=async(url,init)=>{calls.push({url,init});return Response.json({code:0,data:[],meta:{page:{total:0}}});};
    const api=createIamApi("http://api.test",()=>"99",()=>"7");
    await api.roles();
    assert.equal(calls.at(-1).url,"http://api.test/operating-platforms/42/roles");
    await api.members();
    assert.ok(calls.at(-1).url.startsWith("http://api.test/operating-platforms/42/members?"));
    await api.createDepartment({name:"Operations",entity_id:"should-not-be-used"});
    assert.equal(calls.at(-1).url,"http://api.test/operating-platforms/42/departments");
    assert.equal(JSON.parse(calls.at(-1).init.body).entity_id,undefined);
    assert.equal(organizationRouteURL("/roles/51/permission-grants"),"/roles/51/permission-grants");
    assert.equal(organizationRouteURL("/tenants/99/entities"),"/tenants/99/entities");
    window.location.pathname="/admin/99/roles";
    assert.equal(routeTenantID(),"99");
    await api.roles();
    assert.equal(calls.at(-1).url,"http://api.test/tenants/99/roles");
    for(const path of ["/admin/login","/admin/register","/admin/workspaces","/platform/login","/platform/workspaces","/user/files","/system/roles"]){
      window.location.pathname=path;
      assert.equal(routeTenantID(),"");
      assert.equal(operatingPlatformID(),"");
    }
  `,
    ],
    { cwd: import.meta.dir, stdout: "pipe", stderr: "pipe" }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.exitCode).toBe(0)
})

// Every tenant address the platform workspace rewrites must exist for operating
// platforms with the same methods. Walking the published contract keeps a new
// tenant-only route from being silently sent to an address that does not exist.
it("rewrites tenant addresses only to published operating-platform twins", () => {
  const spec = JSON.parse(
    readFileSync(
      join(import.meta.dir, "../../../../../backend-api/docs/openapi.json"),
      "utf8"
    )
  ) as { paths: Record<string, Record<string, unknown>> }
  const concrete = (template: string) =>
    template
      .replace("{platform_id}", "42")
      .replace(/\{[^}]+\}/g, "5")
  const previous = globalThis.window
  globalThis.window = Object.assign(new EventTarget(), {
    location: { pathname: "/platform/42/roles" },
  }) as unknown as Window & typeof globalThis
  try {
    let rewritten = 0
    for (const [template, operations] of Object.entries(spec.paths)) {
      if (!template.startsWith("/tenants/{tenant_id}/")) continue
      const url = concrete(template)
      const result = organizationRouteURL(url)
      if (result === url) continue
      rewritten++
      const twin = template.replace(
        "/tenants/{tenant_id}/",
        "/operating-platforms/{platform_id}/"
      )
      expect(Object.keys(spec.paths[twin] ?? {}), template).toEqual(
        expect.arrayContaining(Object.keys(operations))
      )
      expect(result).toBe(concrete(twin))
    }
    expect(rewritten).toBeGreaterThan(0)
  } finally {
    globalThis.window = previous
  }
})

it("recognizes only snowflake tenant IDs in console paths", () => {
  expect(tenantIDFromPath("/admin/7")).toBe("7")
  expect(tenantIDFromPath("/admin/1000000000000000002/users")).toBe(
    "1000000000000000002"
  )
  expect(tenantIDFromPath("/admin/acme/users")).toBe("")
  expect(tenantIDFromPath("/admin/07/users")).toBe("")
  expect(tenantIDFromPath("/admin/7x")).toBe("")
  expect(tenantIDFromPath("/admin/workspaces")).toBe("")
  expect(tenantIDFromPath("/platform/7")).toBe("")
})

describe("entityIDFromPath", () => {
  // The shell used to match a pattern naming exactly one segment after the
  // entity. /entities/5/files kept the entity perspective and anything longer
  // lost it, so adding a view inside an entity moved somebody into the tenant's
  // perspective the moment they opened it -- the menu changing under them.
  it("names the entity at any depth below it", () => {
    expect(entityIDFromPath("/admin/7/entities/5")).toBe("5")
    expect(entityIDFromPath("/admin/7/entities/5/files")).toBe("5")
    expect(entityIDFromPath("/admin/7/entities/5/departments")).toBe("5")
    expect(entityIDFromPath("/admin/7/entities/5/files/9/preview")).toBe("5")
  })
  it("names no entity where the path addresses none", () => {
    expect(entityIDFromPath("/admin/7")).toBe("")
    expect(entityIDFromPath("/admin/7/entities")).toBe("")
    expect(entityIDFromPath("/admin/7/departments")).toBe("")
    expect(entityIDFromPath("/system/accounts")).toBe("")
    // Not a snowflake, so it addresses nothing -- the same rule the tenant
    // segment follows.
    expect(entityIDFromPath("/admin/7/entities/acme/files")).toBe("")
    expect(entityIDFromPath("/admin/7/entities/0/files")).toBe("")
  })
})
