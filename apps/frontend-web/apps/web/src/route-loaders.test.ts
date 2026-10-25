import { expect, it } from "bun:test"
import { spawnSync } from "node:child_process"
import type { LoaderFunctionArgs } from "react-router"
import { legacyRouteLoader } from "./route-loaders"

function legacy(path: string) {
  const result = legacyRouteLoader({
    request: new Request(`http://console.test${path}`),
  } as LoaderFunctionArgs)
  return result.headers.get("Location")
}

it("moves retired console addresses to their current views", () => {
  expect(legacy("/me/security?tab=mfa#codes")).toBe(
    "/user/security?tab=mfa#codes"
  )
  expect(legacy("/tenants/7/users")).toBe("/admin/7/users")
  expect(legacy("/operating-platforms/3/roles")).toBe("/platform/3/roles")
  expect(legacy("/login")).toBe("/user/login")
  expect(legacy("/login?entry=admin")).toBe("/admin/login?entry=admin")
  expect(legacy("/login/tenant")).toBe("/admin/login")
  expect(legacy("/login/system")).toBe("/system/login")
  expect(legacy("/workspaces/platform")).toBe("/platform/workspaces")
  expect(legacy("/register?entry=system")).toBe("/user/register?entry=system")
  expect(legacy("/recover/tenant")).toBe("/admin/recover")
  expect(legacy("/verify-email?entry=tenant&token=x")).toBe(
    "/admin/verify-email?entry=tenant&token=x"
  )
  expect(legacy("/accept-invitation?token=x")).toBe(
    "/admin/accept-invitation?token=x"
  )
})

it("rejects addresses that have no current view", () => {
  let thrown: unknown
  try {
    legacy("/login/unknown")
  } catch (cause) {
    thrown = cause
  }
  expect(thrown).toBeInstanceOf(Response)
  expect((thrown as Response).status).toBe(404)
})

it("sends the console home to the view the caller can use", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    const saved=new Map();
    globalThis.sessionStorage={getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    globalThis.window=Object.assign(new EventTarget(),{location:{origin:"http://console.test",pathname:"/"},close(){}});
    const {workspaceHomeLoader}=await import("./route-loaders.ts");
    const {iamApi}=await import("./lib/iam-api.ts");
    const {activateAccountView,exitAccountView}=await import("./lib/account-view.ts");
    let access;
    Object.assign(iamApi,{
      myTenants:async()=>access.memberships,
      myPermissions:async()=>access.permissions,
      myOperatingPlatforms:async()=>access.platforms,
      systemPermissions:async()=>{if(access.fail)throw new Error("offline");return access.system},
      memberEntities:async(permission)=>(Array.isArray(permission)?permission:[permission]).flatMap(code=>access.entities[code]??[]),
    });
    const home=async(next,stored)=>{
      access={memberships:[],permissions:[],platforms:[],system:[],entities:{},...next};
      saved.clear();if(stored)saved.set("iam.tenant",stored);
      return (await workspaceHomeLoader()).headers.get("Location");
    };
    const active=(id)=>({id,status:"active"});
    assert.equal(await home({system:["system_view"],memberships:[active("7")]},"7"),"/system");
    assert.equal(await home({platforms:[{id:"3"}],memberships:[active("7")]},"8"),"/platform/3");
    assert.equal(await home({platforms:[{id:"3"}],memberships:[{id:"7",status:"suspended"}]},"7"),"/platform/3");
    assert.equal(await home({platforms:[{id:"3"}],memberships:[active("7")]},"7"),"/admin/7");
    assert.equal(await home({memberships:[{id:"6",status:"suspended"},active("7"),active("8")]},"9"),"/admin/7");
    assert.equal(saved.get("iam.tenant"),"7");
    assert.equal(await home({memberships:[active("7"),active("8")]},"8"),"/admin/8");
    assert.equal(await home({memberships:[active("7")],permissions:["tenant_view"]},"7"),"/system/tenants");
    // An account whose standing is inside one entity lands in that entity's
    // perspective, not the tenant's: the tenant shell is for somebody who
    // administers the tenant.
    assert.equal(
      await home({memberships:[active("7")],entities:{file_view:[{id:"6",status:"active"}]}},"7"),
      "/admin/7/entities/6/files");
    // Any standing at tenant level keeps the tenant perspective, even for
    // somebody who also administers an entity.
    assert.equal(
      await home({memberships:[active("7")],permissions:["role_view"],entities:{entity_view:[{id:"5"}]}},"7"),
      "/admin/7");
    assert.equal(await home({}),"/user");
    assert.equal(await home({fail:true}),"/user");
    globalThis.fetch=async()=>new Response(null,{status:204});
    const token="avt_"+"x".repeat(43);
    const expires=new Date(Date.now()+60000).toISOString();
    for(const [scope_type,scope_id,path] of [
      ["system","","/system"],
      ["platform","3","/platform/3"],
      ["tenant","7","/admin/7"],
      ["entity","5","/admin/7/entities/5/overview"],
      ["personal","9","/user/profile"],
    ]){
      activateAccountView({principal_id:"9",scope_type,scope_id,mode:"read_only"},{id:"50",token,expires_at:expires,mode:"read_only"},"/system/accounts","7");
      assert.equal((await workspaceHomeLoader()).headers.get("Location"),path);
      await exitAccountView();
    }
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})

// Perspective discovery may start before navigation commits, so every lookup
// names the tenant being entered rather than following the address bar.
it("decides the perspective of the tenant being entered", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    const saved=new Map();
    globalThis.sessionStorage={getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    globalThis.window=Object.assign(new EventTarget(),{location:{origin:"http://console.test",pathname:"/"},close(){}});
    const {tenantWorkspaceLoader}=await import("./route-loaders.ts");
    const {iamApi}=await import("./lib/iam-api.ts");
    const {workspaceEntityPermissions}=await import("./lib/navigation.ts");
    let access;
    const asked=[];
    globalThis.fetch=async(input)=>{
      const url=new URL(String(input),"http://console.test");
      if(url.pathname.endsWith("/me"))return Response.json({code:0,data:{subject:"9"}});
      if(url.pathname.endsWith("/client-access"))return Response.json({code:0,data:{external:false,projects:{items:[],has_more:false}}});
      const entities=url.pathname.match(/^.api.tenants.([0-9]+).principals.9.entities$/);
      if(entities){
        asked.push(entities[1]);
        return Response.json({code:0,data:access.entities});
      }
      return new Response(null,{status:404});
    };
    iamApi.myPermissions=async(tenant)=>{
      assert.equal(tenant,access.tenant);
      if(access.fail)throw new Error("offline");
      return access.permissions;
    };
    const enter=async(tenant,next,from="/")=>{
      access={tenant,permissions:[],entities:[],...next};
      asked.length=0;
      window.location.pathname=from;
      const result=tenantWorkspaceLoader({params:{tenantID:tenant}});
      return (await result.workspacePath)||null;
    };
    const active=(id)=>({id,name:"Entity "+id,status:"active"});
    // Any standing at tenant level keeps the tenant's own perspective.
    assert.equal(await enter("2",{permissions:["user_view"],entities:[active("5")]}),null);
    assert.deepEqual(asked,[]);
    // Nothing at tenant level and an entity that admits the caller.
    assert.equal(await enter("2",{entities:[active("5")]}),"/admin/2/entities/5/files");
    // Project roles may explicitly open the scoped Home without acquiring
    // tenant permissions. Generic entity-only accounts keep their default.
    assert.equal(await enter("2",{entities:[{...active("5"),type:"project"}]}),null);
    // The switcher offers only active entities, so the loader lands in one.
    assert.equal(
      await enter("2",{entities:[{id:"4",name:"Old",status:"suspended"},active("5")]}),
      "/admin/2/entities/5/files");
    assert.equal(await enter("2",{entities:[{id:"4",name:"Old",status:"suspended"}]}),null);
    // A failed lookup moves nobody.
    assert.equal(await enter("2",{fail:true,entities:[active("5")]}),null);
    assert.deepEqual(asked,[]);
    // Leaving another tenant or a platform: the entity listing still asks about
    // the tenant being entered.
    assert.equal(
      await enter("2",{entities:[active("5")]},"/admin/1/users"),
      "/admin/2/entities/5/files");
    assert.deepEqual(asked,["2"]);
    assert.equal(
      await enter("2",{entities:[active("5")]},"/platform/123/roles"),
      "/admin/2/entities/5/files");
    assert.deepEqual(asked,["2"]);
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})

it("commits the copied tenant-admin workspace before its real permission shape resolves", () => {
  const result = spawnSync(process.execPath, ["--eval", `
    import assert from "node:assert/strict";
    import { readFileSync } from "node:fs";
    import { createMemoryRouter } from "react-router";
    const fixture=JSON.parse(readFileSync("../tests/fixtures/tenant-admin-permissions.json","utf8"));
    const saved=new Map();
    globalThis.sessionStorage={getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    globalThis.window=Object.assign(new EventTarget(),{location:{origin:"http://console.test",pathname:"/admin/workspaces"},close(){}});
    const {tenantWorkspaceLoader}=await import("./route-loaders.ts");
    let complete;
    const permissions=new Promise(resolve=>{complete=resolve});
    const asked=[];
    globalThis.fetch=async(input)=>{
      const path=new URL(String(input),"http://console.test").pathname;
      asked.push(path);
      if(path==="/api/me")return Response.json({code:0,data:{subject:fixture.subject,organization_id:fixture.tenantID}});
      if(path==="/api/tenants/"+fixture.tenantID+"/principals/"+fixture.subject+"/permissions") {
        await permissions;
        return Response.json(fixture.response);
      }
      throw new Error("unexpected discovery: "+path);
    };
    const destination="/admin/"+fixture.tenantID;
    const router=createMemoryRouter([
      {path:"/admin/workspaces"},
      {path:"/admin/:tenantID",loader:tenantWorkspaceLoader},
    ],{initialEntries:["/admin/workspaces"]});
    try {
      await Promise.race([router.navigate(destination),new Promise(resolve=>setTimeout(resolve,150))]);
      assert.equal(router.state.location.pathname,destination,"slow permission discovery must not hold the chooser URL");
      assert.equal(router.state.navigation.state,"idle");
      const {workspacePath}=Object.values(router.state.loaderData)[0];
      assert(workspacePath instanceof Promise);
      complete();
      assert.equal(await workspacePath,"","the real tenant-admin response retains tenant perspective");
      assert.deepEqual(asked,["/api/me","/api/tenants/"+fixture.tenantID+"/principals/"+fixture.subject+"/permissions"]);
    } finally {complete();router.dispose()}
  `], {cwd: import.meta.dir, stdio: "pipe", windowsHide: true})
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})
