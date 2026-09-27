import { expect, it } from "bun:test"

it("selects operating collection routes while preserving owned and tenant boundaries", () => {
  const result = Bun.spawnSync(
    [
      process.execPath,
      "--eval",
      `
    import assert from "node:assert/strict";
    import {createIamApi} from "./iam-api.ts";
    import {organizationRouteURL} from "./organization-scope.ts";
    globalThis.window = Object.assign(new EventTarget(), {location:{pathname:"/operating-platforms/42/roles"}});
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
    window.location.pathname="/tenants/99/roles";
    await api.roles();
    assert.equal(calls.at(-1).url,"http://api.test/tenants/99/roles");
  `,
    ],
    { cwd: import.meta.dir, stdout: "pipe", stderr: "pipe" }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.exitCode).toBe(0)
})
