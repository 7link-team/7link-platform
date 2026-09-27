import { expect, it } from "bun:test"

it("isolates view credentials, identity caches and workspace storage", () => {
  const result = Bun.spawnSync(
    [
      process.execPath,
      "--eval",
      `
    import assert from "node:assert/strict";
    import {activateAccountView,getAccountView,issueAccountView,exitAccountView} from "./account-view.ts";
    import {createIamApi,getTenant,setTenant,adoptSessionTenant} from "./iam-api.ts";
    const saved = new Map([["iam.tenant","operator-tenant"],["iam.tenant:subject","1"]]);
    globalThis.localStorage = {getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    globalThis.window = Object.assign(new EventTarget(), {location:{origin:"http://console.test",replace:path=>{assert.equal(path,"/system/accounts");}}});
    const calls = [];
    const token = "avt_"+"x".repeat(43);
    globalThis.fetch = async (url,init) => {
      calls.push({url,init});
      if (url.endsWith("/account-views")) return Response.json({data:{id:"50",token,expires_at:new Date(Date.now()+60000).toISOString(),mode:"read_only"}});
      if (url.endsWith("/avatar/content?tenant_id=90")) return new Response("image");
      return Response.json({code:0,data:url.endsWith("/me")?{subject:getAccountView()?"9":"1"}:{revoked:true}});
    };
    const api = createIamApi("http://api.test",()=>"90");
    await api.session();
    await api.myAvatar();
    assert.equal(calls.at(-1).url,"http://api.test/principals/1/avatar");
    const selection = {principal_id:"9",scope_type:"personal",scope_id:"9",mode:"read_only"};
    const issued = await issueAccountView(selection);
    assert.equal(calls.at(-1).init.credentials,"include");
    assert.equal(new Headers(calls.at(-1).init.headers).get("Authorization"),null);
    assert.equal(new Headers(calls.at(-1).init.headers).get("Origin"),"http://console.test");
    activateAccountView(selection,issued,"/system/accounts","");
    assert.ok(!JSON.stringify(getAccountView()).includes(token));
    const verification = api.session();
    const avatar = api.myAvatar();
    assert.equal((await verification).subject,"9");
    await avatar;
    await api.myAvatar();
    assert.equal(calls.at(-1).url,"http://api.test/principals/9/avatar");
    assert.equal(calls.at(-1).init.credentials,"omit");
    assert.equal(new Headers(calls.at(-1).init.headers).get("Authorization"),"Bearer "+token);
    await api.avatarContent("9","90");
    assert.equal(calls.at(-1).init.credentials,"omit");
    assert.equal(new Headers(calls.at(-1).init.headers).get("Authorization"),"Bearer "+token);
    const count=calls.length;
    await assert.rejects(api.createPrincipal({login_name:"forbidden",password:"aA1!"}),/read-only/);
    assert.equal(calls.length,count);
    setTenant("view-tenant");
    adoptSessionTenant({subject:"9",organization_id:"different"});
    assert.equal(getTenant(),"view-tenant");
    assert.equal(saved.get("iam.tenant"),"operator-tenant");
    assert.equal(saved.get("iam.tenant:subject"),"1");
    await exitAccountView();
    assert.equal(calls.at(-1).init.method,"DELETE");
    assert.equal(calls.at(-1).init.credentials,"include");
    assert.equal(new Headers(calls.at(-1).init.headers).get("Authorization"),null);
  `,
    ],
    { cwd: import.meta.dir, stdout: "pipe", stderr: "pipe" }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.exitCode).toBe(0)
})
