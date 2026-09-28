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
    });
    const home=async(next,stored)=>{
      access={memberships:[],permissions:[],platforms:[],system:[],...next};
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
