// Fixture browser smoke; does not establish backend or authorization acceptance.
// Run with Vite listening on IAM_CONSOLE_URL (default: http://127.0.0.1:4303).
import { execFileSync } from "node:child_process"
import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4303"
const session = `page-migration-${process.pid}`
const source = readFileSync(
  new URL("../src/router.tsx", import.meta.url),
  "utf8"
)
const pages = [
  ...source.matchAll(/import\("(\.\/app\/[^"\n]+)"\)\)\s*\.\s*(\w+)/g),
]
  .map((match) => ({ path: `/src/${match[1].slice(2)}.tsx`, export: match[2] }))
  .filter(
    (page, index, all) =>
      all.findIndex(
        (other) => other.path === page.path && other.export === page.export
      ) === index
  )
const moduleCount = new Set(pages.map((page) => page.path)).size
if (moduleCount !== 42)
  throw new Error(`Router coverage changed: found ${moduleCount} modules`)
pages.push(
  {
    path: "/src/app/security/page.tsx",
    export: "default",
    route: "/user/profile",
  },
  { path: "/src/app/security/page.tsx", export: "default", route: "/user/mfa" }
)
pages.push({path:"/src/app/projects/detail.tsx",export:"default",route:"/admin/1/entities/1/project",shell:true})
const batchSize = 18
if (!process.env.PAGE_AUDIT_MATCH && process.env.PAGE_AUDIT_BATCH === undefined) {
  for (let batch = 0; batch < Math.ceil(pages.length / batchSize); batch++) {
    const output = execFileSync(process.execPath, [fileURLToPath(import.meta.url)], {
      env: { ...process.env, PAGE_AUDIT_BATCH: String(batch) },
      encoding: "utf8",
      windowsHide: true,
      timeout: 60000,
      maxBuffer: 4 * 1024 * 1024,
    })
    process.stdout.write(output)
  }
  process.exit(0)
}
const matchedPages = process.env.PAGE_AUDIT_MATCH
  ? pages.filter(page => page.path.includes(process.env.PAGE_AUDIT_MATCH))
  : pages
// Bound each browser eval below the CLI daemon's response deadline on Windows.
const auditPages = process.env.PAGE_AUDIT_BATCH === undefined
  ? matchedPages
  : matchedPages.slice(Number(process.env.PAGE_AUDIT_BATCH) * batchSize, (Number(process.env.PAGE_AUDIT_BATCH) + 1) * batchSize)
const shared = (path) =>
  "/@fs/" +
  fileURLToPath(new URL(`../../../packages/ui/src/${path}`, import.meta.url))
    .replaceAll("\\", "/")
    .replace(/^\//, "")
const transformed = await Promise.all(
  ["/src/main.tsx", "/src/router.tsx", shared("i18n/intl-provider.tsx")].map(
    async (path) => (await fetch(`${baseURL}${path}`)).text()
  )
)
const dependency = (name) => {
  const path = [
    ...transformed.join("\n").matchAll(/"([^" ]+\.js\?v=[a-z0-9]+)"/g),
  ]
    .map((match) => match[1])
    .find((path) => path.includes(`/${name}.js?`))
  if (!path) throw new Error(`Missing Vite dependency: ${name}`)
  return JSON.stringify(path)
}
const run = (...args) =>
  execFileSync("bun", ["x", "agent-browser", "--session", session, ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 60000,
  })
try {
  // Start on a stable anonymous route: the live root redirect must not race
  // with the fixture's temporary session/API replacements.
  run("open", `${baseURL}/admin/login`)
  run("wait", "#login-name")
  run("snapshot", "-i")
  const script = String.raw`(async () => {
    const React = (await import(${dependency("react")})).default;
    const {createRoot} = (await import(${dependency("react-dom_client")})).default;
    const {createMemoryRouter,RouterProvider,Outlet} = await import(${dependency("react-router")});
    const {IntlAppProvider} = await import(${JSON.stringify(shared("i18n/intl-provider.tsx"))});
    const {ThemeProvider} = await import('/src/components/theme-provider.tsx');
    const {AuthContext} = await import('/src/components/auth.ts');
    const {loadMessages} = await import('/src/lib/i18n.ts');
    const {iamApi} = await import('/src/lib/iam-api.ts');
    const messages = await loadMessages('en-US');
    const fixtureSession = {subject:'1',issuer:'fixture',acr:2,preferred_username:'Fixture',email:'fixture@example.invalid',email_verified:true};
    const emptyPage = {items:[],total:0};
    const reads = {};
    for (const name of 'groups departments entities menus roles members sessions passkeys oauthConsents identityLinks identityLinkProviders myTenants myOperatingPlatforms personalTokenPermissions permissions myPermissions effectiveMenus systemNavigation operatingNavigation operatingPermissions systemPermissions invitations serviceAccounts oauthClients myAccessRequests requestableRoles accessRequests accessReviews systemAccountOwners systemMembers systemDepartments systemRoles systemOwnerMembers systemOwnerDepartments systemOwnerRoles entityRoleBindings resourceRelationships relationships groupMembers memberEntities roleDirectoryBindings roleMemberGrants rolePermissionGrants fileCollections accountFileCollections systemFileCollections'.split(' ')) reads[name]=[];
    for (const name of 'principals systemPrincipals tenantPage personalTokens personalMemberships closureRequests auditEvents systemAuditEvents operatingPlatforms'.split(' ')) reads[name]=emptyPage;
    Object.assign(reads,{serviceAccounts:emptyPage,operatingPlatforms:[],systemMenus:[],systemRolePermissionCatalog:[],myAvatar:{mode:'generated'},files:{items:[]},closureRequest:null,systemAuditIntegrity:{valid:true,verified_events:0,head_sequence:0},tenantRegistrationPolicy:{enabled:false},entity:{id:'1',name:'Fixture entity',type:'organization',status:'active',version:1},mfaStatus:{totp_enabled:false,recovery_codes_remaining:0},capabilities:{password:true,registration:true,captcha:{enabled:false},phone:{enabled:false},passkey:{enabled:false}},deployment:{name:'Fixture'}});
    reads.entity.metadata={};reads.entity.parent_id='';reads.entities=[reads.entity];reads.auditIntegrity=reads.systemAuditIntegrity;
    reads.myAccessRequests=Array.from({length:240},(_,i)=>({id:'request-'+i,requester_id:'1',role_id:'role-1',role_name:'Fixture role',reason:'Request '+i,status:'pending',access_expires_at:'2027-01-01T00:00:00Z'}));
    reads.sessionForTenant=fixtureSession;
    reads.project={tenant_id:'1',project_id:'1',name:'Fixture project <script>',code:'PROJECT-1',description:'Real API contract fixture',stage:'ACTIVE',team_mode:'HYBRID',health:'GREEN',client:null,lead_agent_id:null,target_date:'2026-12-01',version:1,created_at:'2026-10-18T08:00:00Z',updated_at:'2026-10-18T09:00:00Z',archived_at:null,blocked_reason:null};
    let calls=[],missing=[],errors=[];
    const originalConsoleError=console.error;
    console.error=(...args)=>{if(args.some(value=>String(value).includes('Maximum update depth exceeded')))errors.push(args.map(String).join(' ')+new Error().stack);originalConsoleError(...args)};
    for (const name of Object.keys(iamApi)) {
      if(typeof iamApi[name]!=='function')continue;
      iamApi[name]=async()=>{calls.push(name);if(Object.hasOwn(reads,name))return structuredClone(reads[name]);missing.push(name);throw Error('Fixture missing: '+name)};
    }
    let generatedStyle;
    iamApi.avatarCandidates=async(style)=>{generatedStyle=style;return []};
    // Explicit empty collections also cover pages constructing their own API.
    // Unknown requests are denied and reported, never silently treated as loaded.
    const originalFetch=window.fetch;
    window.fetch=async(input,init)=>{
      const url=String(input instanceof Request?input.url:input);
      if(new URL(url,location.href).pathname.startsWith('/api/')){
        const path=new URL(url,location.href).pathname;
        const policy={tenant_id:'1',min_days:365,archive_after_days:730};
        const special=path.endsWith('/effective-fields')?{resource:'user_profile',fields:{}}:path.endsWith('/audit/governance-policy')?{policy,integrity:{valid:true,verified_events:0,head_sequence:0},anchors:[],total_events:0,archive_ready_events:0,anchored_events:0}:path.endsWith('/audit/archive-status')?{enabled:false,policy,total_events:0,eligible_events:0,archived_events:0,pending_events:0,queued_jobs:0,running_jobs:0,failed_jobs:0,completed_jobs:0}:undefined;
        if((init?.method??'GET')==='GET'&&special!==undefined)return new Response(JSON.stringify({code:0,message:'ok',data:special}),{headers:{'Content-Type':'application/json'}});
        if((init?.method??'GET')==='GET' && /\/(principals|members|departments|field-policies|field-catalog|roles|scim-directories|archives)$/.test(path)){
          return new Response(JSON.stringify({code:0,message:'ok',data:[],meta:{page:{total:0}}}),{headers:{'Content-Type':'application/json'}});
        }
        missing.push('HTTP '+url);
        return new Response(JSON.stringify({code:'fixture_unavailable',message:'Fixture unavailable'}),{status:503,headers:{'Content-Type':'application/json'}});
      }
      return originalFetch(input,init);
    };
    const capture=(event)=>{errors.push(String(event.error?.stack??event.reason?.stack??event.message??event.reason));event.preventDefault()};
    window.addEventListener('error',capture);window.addEventListener('unhandledrejection',capture);
    class Boundary extends React.Component{state={error:null};static getDerivedStateFromError(error){return {error}};componentDidCatch(error){errors.push(String(error.stack??error))};render(){return this.state.error?React.createElement('pre',{'data-smoke-error':true},String(this.state.error)):this.props.children}}
    const outlet={menus:[],permissions:{has:()=>true},permissionsResolved:true,tenantID:'1',tenantName:'Fixture',entityID:'1',entityNames:{'1':'Fixture'}};
    const tick=()=>new Promise(resolve=>setTimeout(resolve,100));
    const results=[];
    for(const page of ${JSON.stringify(auditPages)}){
      calls=[];missing=[];errors=[];
      const node=document.createElement('div');document.body.append(node);const root=createRoot(node);
      let router;
      const originalTenants=iamApi.myTenants, originalPermissions=iamApi.myPermissions;
      try{
        const module=await import(page.path);const Component=module[page.export];
        if(typeof Component!=='function')throw Error('Missing component '+page.export);
        const anonymous=/\/(register|recover|verify-email|accept-invitation|login\/page)/.test(page.path);
        const auth={session:anonymous?null:fixtureSession,status:anonymous?'anonymous':'authenticated',refresh:async()=>{},logout:async()=>{}};
        const location=page.route??(page.path.includes('/system/')||page.export.startsWith('System')?'/system/fixture':page.export.startsWith('Platform')?'/platform/1/fixture':page.path.includes('/account/')?'/account/fixture':page.path.includes('/security/')?'/user/security':'/admin/1/entities/1/overview');
        let shell;
        if(page.shell){
          shell=(await import('/src/app/layout.tsx')).default;
          iamApi.myTenants=async()=>[{id:'1',name:'Fixture tenant',slug:'fixture',status:'active'},{id:'2',name:'Second tenant',slug:'second',status:'active'}];
          iamApi.myPermissions=async()=>['project_read'];
        }
        router=createMemoryRouter([{element:page.shell?React.createElement(shell):React.createElement(Outlet,{context:outlet}),children:[{path:location.includes('/admin/')?'/admin/:tenantID/entities/:entityID/:view':location,element:React.createElement(Component),children:[{path:'*',element:React.createElement('span',null,'Fixture outlet')}]},{path:'*',element:React.createElement('span',{'data-fixture-redirect':true},'Fixture redirect destination')}]}],{initialEntries:[location]});
        root.render(React.createElement(Boundary,null,React.createElement(IntlAppProvider,{initialLocale:'en-US',initialMessages:messages,loadMessages:async()=>messages,timeZone:'UTC'},React.createElement(ThemeProvider,null,React.createElement(AuthContext.Provider,{value:auth},React.createElement(RouterProvider,{router}))))));
        await tick();await tick();await tick();
        if(!node.textContent.trim())errors.push('Empty rendered page');
        if(node.textContent.includes('Unexpected Application Error!'))errors.push('Router error boundary: '+node.textContent);
        const slowRead = {'/src/app/account/memberships.tsx':'personalMemberships','/src/app/account/tokens.tsx':'personalTokens','/src/app/account/identities.tsx':'identityLinks','/src/app/principals/closure-requests.tsx':'closureRequests'}[page.path];
        if(slowRead){
          const previous=iamApi[slowRead], originalInterval=window.setInterval, originalClear=window.clearInterval, originalNow=Date.now;
          let finish, count=0, scheduled;let clock=originalNow();
          iamApi[slowRead]=()=>{count++;return new Promise(resolve=>{finish=()=>resolve(structuredClone(reads[slowRead]))})};
          window.setInterval=(callback,ms,...args)=>ms===1000?(scheduled=callback,-100):originalInterval(callback,ms,...args);
          window.clearInterval=id=>{if(id!==-100)originalClear(id)};Date.now=()=>clock;
          try{
            const refreshTrigger=node.querySelector('[aria-label="Auto refresh"]');
            if(!refreshTrigger)throw Error('Missing page refresh: '+node.textContent.slice(-1500));
            refreshTrigger.click();await tick();
            const option=[...document.querySelectorAll('[role=menuitemradio]')].find(item=>item.textContent.includes('5 seconds'));
            if(!option)throw Error('Missing 5-second refresh option');option.click();await tick();
            clock+=6000;scheduled();await tick();
            if(count!==1||!node.querySelector('[aria-label="Refresh"]')?.disabled)throw Error('Refresh not busy during actual page read');
            clock+=6000;scheduled();await tick();if(count!==1)throw Error('Overlapping page read');
            finish();await tick();if(node.querySelector('[aria-label="Refresh"]')?.disabled)throw Error('Refresh stayed locked after read');
            clock+=6000;scheduled();await tick();if(count!==2)throw Error('Next scheduled refresh did not run');finish();await tick();
          }finally{iamApi[slowRead]=previous;window.setInterval=originalInterval;window.clearInterval=originalClear;Date.now=originalNow}
        }
        if(page.path==='/src/app/projects/detail.tsx'){
          const previous=iamApi.project;
          const refresh=()=>node.querySelector('[aria-label="Refresh"]').click();
          const form=()=>node.querySelector('form');
          if(!form()||form().querySelector('input,textarea,select,button[type=submit]'))throw Error('Project detail is not a readonly AutoForm: '+node.textContent.slice(-300));
          if(!node.textContent.includes('Human and agent')||!node.textContent.includes('Healthy'))errors.push('Project enum labels missing');
          if(node.querySelector('script')?.textContent.includes('Fixture project'))errors.push('Project title injected markup');
          try{
            iamApi.project=async()=>{throw Error('Project access denied fixture')};refresh();await tick();await tick();
            if(form()||!node.textContent.includes('Project access denied fixture'))throw Error('Failed project refresh retained accessible details');
            iamApi.project=previous;refresh();await tick();await tick();if(!form())throw Error('Project retry did not recover');
            const pending=new Map();
            iamApi.project=(id,signal)=>new Promise(resolve=>pending.set(id,{signal,resolve}));
            refresh();await tick();
            await router.navigate('/admin/1/entities/2/project');await tick();
            if(!pending.get('1')?.signal.aborted||form())throw Error('Project switch retained old read or form');
            pending.get('2').resolve({...reads.project,project_id:'2',name:'Second project',code:'PROJECT-2'});await tick();await tick();
            pending.get('1').resolve({...reads.project,name:'Obsolete project'});await tick();
            if(!node.textContent.includes('Second project')||node.textContent.includes('Obsolete project'))throw Error('Stale project response crossed scope');
            node.style.width='390px';await tick();if(node.scrollWidth>node.clientWidth+1)throw Error('Project detail horizontal overflow');
            await router.navigate('/admin/2/entities/3/project');await tick();pending.get('3').resolve(reads.project);await tick();await tick();
            if(form()||!node.textContent.includes('unavailable in the current workspace'))throw Error('Project tenant mismatch was displayed');
          }finally{iamApi.project=previous}
        }
        let createDialog='not-targeted';
        if(/\/(groups|departments|entities|menus|tenants)\/page.tsx$/.test(page.path)){
          const trigger=[...node.querySelectorAll('button')].find(button=>button.querySelector('.lucide-plus'));
          if(!trigger)errors.push('Missing create dialog trigger');
          else{trigger.click();await tick();await tick();const dialog=document.querySelector('[role=dialog]');if(!dialog?.querySelector('form')||!dialog.querySelector('input'))errors.push('Create dialog form did not render');else{const input=dialog.querySelector('#department-name')??dialog.querySelector('input:not([type=hidden]):not([type=number]):not([role=combobox])');if(input){Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'Fixture edited');input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));await tick();if(input.value!=='Fixture edited')errors.push('Create field edit was lost');}createDialog='rendered-and-edited';}}
        }
        let accessRequestPagination;
        if(page.path==='/src/app/access-requests/page.tsx'){
          const pager=node.querySelector('[data-slot="pagination"]');
          if(!pager?.querySelector('[aria-label="Page 12"]')||!pager.querySelector('[data-slot="pagination-ellipsis"]'))errors.push('Access requests numeric pages/ellipsis missing');
          else{
            pager.querySelector('[aria-label="Page 12"]').click();await tick();await tick();
            if(node.querySelector('tbody [data-column="sequence"]')?.textContent!=='221')errors.push('Access requests last-page rows incorrect');
            if(!node.querySelector('[aria-label="Page 12"][aria-current="page"]'))errors.push('Access requests active page missing');
            if(!node.querySelector('[aria-label="Next page"]')?.disabled)errors.push('Access requests last-page next enabled');
            node.querySelector('[aria-label="Page 1"]').click();await tick();await tick();
            if(node.querySelector('tbody [data-column="sequence"]')?.textContent!=='1')errors.push('Access requests first-page navigation incorrect');
            if(!node.querySelector('[aria-label="Auto refresh"]'))errors.push('Access requests split refresh missing');
            accessRequestPagination='240 rows;12 pages;ellipsis;first/last navigation;active page;split refresh';
          }
        }
        let avatarStyle;
        if(page.route==='/user/profile'){
          const pixel=node.querySelector('input[type=radio][value=pixel], [role=radio][value=pixel]');
          if(!pixel)errors.push('Pixel avatar style radio missing');
          else{pixel.click();await tick();if(!pixel.checked&&pixel.getAttribute('aria-checked')!=='true')errors.push('Pixel avatar radio did not update');const generate=[...node.querySelectorAll('button')].find(button=>button.querySelector('.lucide-refresh-cw'));if(!generate)errors.push('Avatar generate button missing');else{generate.click();await tick();if(generatedStyle!=='pixel')errors.push('Avatar generate used stale style '+generatedStyle);else avatarStyle='pixel forwarded';}}
        }
        results.push({module:page.path,export:page.export,route:page.route,avatarStyle,accessRequestPagination,state:missing.length?'fixture-error':node.querySelector('[data-fixture-redirect]')?'redirect':'rendered',forms:node.querySelectorAll('form').length,tables:node.querySelectorAll('table').length,createDialog,reads:[...new Set(calls)],missing:[...new Set(missing)],errors:[...errors]});
      }catch(error){results.push({module:page.path,errors:[String(error.stack??error)]})}
      finally{root.unmount();router?.dispose();node.remove();iamApi.myTenants=originalTenants;iamApi.myPermissions=originalPermissions;await tick()}
    }
    console.error=originalConsoleError;window.fetch=originalFetch;window.removeEventListener('error',capture);window.removeEventListener('unhandledrejection',capture);
    return {pass:results.every(result=>result.errors.length===0 && result.missing?.length===0),uniqueModules:new Set(results.map(result=>result.module)).size,exports:new Set(results.map(result=>result.module+'#'+result.export)).size,cases:results.length,avatarStyle:results.find(result=>result.route==='/user/profile')?.avatarStyle,rendered:results.filter(result=>result.state==='rendered').length,fixtureErrors:results.filter(result=>result.state==='fixture-error').length,redirects:results.filter(result=>result.state==='redirect').length,limitations:['Read fixtures only; no backend writes, populated row actions, authorization, or backend acceptance.'],results};
  })()`
  const output = execFileSync(
    "bun",
    ["x", "agent-browser", "--session", session, "eval", "--stdin"],
    {
      input: script,
      encoding: "utf8",
      windowsHide: true,
      timeout: 60000,
      maxBuffer: 4 * 1024 * 1024,
    }
  )
  const report = JSON.parse(output)
  console.log(
    JSON.stringify(
      {
        ...report,
        accessRequestPagination: report.results.find(
          (result) => result.accessRequestPagination
        )?.accessRequestPagination,
        createDialogs: report.results
          .filter((result) => result.createDialog === "rendered-and-edited")
          .map((result) => `${result.module}#${result.export}`),
        coverage: report.results.map(
          (result) => `${result.module}#${result.export}: ${result.state}`
        ),
        results: report.results.filter(
          (result) => result.errors.length || result.missing?.length
        ),
      },
      null,
      2
    )
  )
  if (!report.pass) process.exitCode = 1
} finally {
  run("close")
}
