import { expect, it } from "bun:test"
import { spawnSync } from "node:child_process"

it("isolates view credentials, identity caches and workspace storage", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    import {activateAccountView,getAccountView,issueAccountView,exitAccountView} from "./account-view.ts";
    import {createIamApi,getTenant,setTenant,adoptSessionTenant} from "./iam-api.ts";
    const saved = new Map([["iam.tenant","operator-tenant"],["iam.tenant:subject","1"]]);
    globalThis.sessionStorage = {getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    let closed=false;
    globalThis.window = Object.assign(new EventTarget(), {close:()=>{closed=true},location:{origin:"http://console.test"}});
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
    assert.equal(getTenant(),"operator-tenant");
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
    assert.equal(closed,true);
    await assert.rejects(api.session(),/closed/);
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})

it("accepts one verified child handoff and loses authorization on document disposal", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    const saved=new Map();
    globalThis.sessionStorage={getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)};
    const sent=[];
    const opener={postMessage:(data,origin)=>sent.push({data,origin})};
    globalThis.window=Object.assign(new EventTarget(),{
      location:{origin:"http://console.test",pathname:"/account-view/open"},
      name:"iam-account-view:nonce",opener,setTimeout,clearTimeout,
      history:{replaceState:(_state,_title,path)=>{assert.equal(path,"/admin/7");window.location.pathname=path}}
    });
    const {initializeAccountViewTab,getAccountView,accountViewRequest}=await import("./account-view.ts");
    const initialization=initializeAccountViewTab();
    assert.deepEqual(sent[0],{data:{type:"account-view-ready",nonce:"nonce"},origin:"http://console.test"});
    const token="avt_"+"y".repeat(43);
    const payload={type:"account-view-activate",nonce:"nonce",selection:{principal_id:"9",scope_type:"tenant",scope_id:"7",mode:"read_only"},issued:{id:"50",token,expires_at:new Date(Date.now()+60000).toISOString(),mode:"read_only"},returnPath:"/system/accounts",tenantID:"7"};
    const emit=(data,source=opener,origin="http://console.test")=>{
      const event=new Event("message");Object.assign(event,{data,source,origin});window.dispatchEvent(event);
    };
    emit(payload,{});emit(payload,opener,"http://other.test");emit({...payload,nonce:"wrong"});
    assert.equal(getAccountView(),null);
    assert.throws(()=>accountViewRequest("GET"),/closed/);
    emit(payload);
    assert.equal(await initialization,"active");
    assert.equal(getAccountView().principal_id,"9");
    assert.equal(accountViewRequest("GET").credentials,"omit");
    assert.equal(new Headers(accountViewRequest("GET").headers).get("Authorization"),"Bearer "+token);
    assert.equal(window.opener,null);assert.equal(window.name,"iam-account-view:closed");
    assert.equal(sent.at(-1).data.type,"account-view-accepted");
    assert.equal(saved.get("iam.account-view-tab"),"1");
    assert.ok(!JSON.stringify([...saved]).includes(token));
    emit({...payload,selection:{...payload.selection,principal_id:"8"}});
    assert.equal(getAccountView().principal_id,"9");
    window.dispatchEvent(new Event("pagehide"));
    assert.equal(getAccountView(),null);
    assert.throws(()=>accountViewRequest("GET"),/closed/);
    assert.equal(await initializeAccountViewTab(),"closed");
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})

it("blocks refreshed view tabs even when browser storage is unavailable", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    globalThis.sessionStorage={getItem:()=>{throw new Error("Storage disabled")},setItem:()=>{throw new Error("Storage disabled")}};
    globalThis.window=Object.assign(new EventTarget(),{location:{pathname:"/admin/7"},name:"iam-account-view:closed"});
    const {initializeAccountViewTab,accountViewRequest,issueAccountView}=await import("./account-view.ts");
    assert.equal(await initializeAccountViewTab(),"closed");
    assert.throws(()=>accountViewRequest("GET"),/closed/);
    await assert.rejects(issueAccountView({principal_id:"9",scope_type:"tenant",scope_id:"7",mode:"read_only"}),/already open/);
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})

it("keeps the operator unchanged and revokes failed popup transfers", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--eval",
      `
    import assert from "node:assert/strict";
    globalThis.sessionStorage={getItem:()=>null};
    const calls=[],transfers=[];let popup,blocked=false,failDelivery=false;
    const emit=(data,source)=>{const event=new Event("message");Object.assign(event,{origin:"http://console.test",source,data});window.dispatchEvent(event)};
    globalThis.window=Object.assign(new EventTarget(),{
      location:{origin:"http://console.test",pathname:"/system/accounts"},
      setTimeout:(fn,ms)=>setTimeout(fn,Math.min(ms,100)),clearTimeout,
      setInterval:fn=>setInterval(fn,1),clearInterval,
      open:(path,name)=>{
        assert.equal(path,"/account-view/open");if(blocked)return null;
        const nonce=name.split(":")[1];
        popup={closed:false,close:()=>{popup.closed=true},postMessage:(data,origin)=>{
          assert.equal(origin,"http://console.test");transfers.push(structuredClone(data));
          if(failDelivery){popup.closed=true;return}
          emit({type:"account-view-accepted",nonce},{});
          queueMicrotask(()=>emit({type:"account-view-accepted",nonce},popup));
        }};
        queueMicrotask(()=>emit({type:"account-view-ready",nonce},popup));return popup;
      }
    });
    globalThis.fetch=async(url,init)=>{calls.push({url,init});return Response.json({data:{id:"50",token:"avt_"+"z".repeat(43),expires_at:new Date(Date.now()+60000).toISOString(),mode:"read_only"}})};
    const {openAccountViewTab,getAccountView,accountViewRequest}=await import("./account-view.ts");
    const selection={principal_id:"9",scope_type:"tenant",scope_id:"7",mode:"read_only"};
    await openAccountViewTab(selection,"/system/accounts","7");
    assert.equal(transfers.length,1);assert.equal(transfers[0].selection.principal_id,"9");
    assert.ok(transfers[0].issued.token.startsWith("avt_"));
    assert.equal(getAccountView(),null);assert.equal(accountViewRequest("GET").credentials,"include");
    assert.equal(calls.length,1);assert.equal(calls[0].init.credentials,"include");
    blocked=true;
    await assert.rejects(openAccountViewTab(selection,"/system/accounts","7"));
    assert.equal(calls.length,1);
    blocked=false;failDelivery=true;
    await assert.rejects(openAccountViewTab(selection,"/system/accounts","7"));
    assert.equal(calls.at(-1).init.method,"DELETE");assert.ok(calls.at(-1).url.endsWith("/account-views/50"));
    assert.equal(calls.at(-1).init.credentials,"include");
    assert.equal(getAccountView(),null);
  `,
    ],
    { cwd: import.meta.dir, stdio: "pipe", windowsHide: true }
  )
  expect(result.stderr.toString()).toBe("")
  expect(result.status).toBe(0)
})
