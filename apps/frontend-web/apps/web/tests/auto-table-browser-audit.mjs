// Run against the Web Vite dev server. Tests shared tables with controlled transports, not API integration.
// IAM_CONSOLE_URL=http://127.0.0.1:4303 bun tests/auto-table-browser-audit.mjs
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4303"
const session = `auto-table-${process.pid}`
const hookPath =
  "/@fs" +
  fileURLToPath(
    new URL(
      "../../../packages/ui/src/components/auto-form/use-auto-form.ts",
      import.meta.url
    )
  ).replaceAll("\\", "/")
const main = await (await fetch(`${baseURL}/src/main.tsx`)).text()
const hook = await (await fetch(`${baseURL}${hookPath}`)).text()
const dependency = (name) => {
  const path = [...`${main}\n${hook}`.matchAll(/"([^" ]+\.js\?v=[a-z0-9]+)"/g)]
    .map((match) => match[1])
    .find((path) => path.includes(`/${name}.js?`))
  if (!path) throw new Error(`Missing Vite dependency: ${name}`)
  return JSON.stringify(path)
}
const run = (...args) =>
  execFileSync("bunx", ["agent-browser", "--session", session, ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 60000,
  })
try {
  run("open", baseURL)
  const script = `(async () => {
    const React = (await import(${dependency("react")})).default;
    const {createRoot} = (await import(${dependency("react-dom_client")})).default;
    const {flushSync} = (await import('/node_modules/.vite/deps/react-dom.js')).default;
    const {IntlProvider} = await import(${dependency("use-intl")});
    const {CommonTable} = await import(${JSON.stringify(hookPath.replace("/auto-form/use-auto-form.ts", "/crud/common-table.tsx"))});
    const {AutoTable} = await import(${JSON.stringify(hookPath.replace("/auto-form/use-auto-form.ts", "/auto-table/auto-table.tsx"))});
    const {ApiProvider} = await import(${JSON.stringify(hookPath.replace("/auto-form/use-auto-form.ts", "/api-provider.tsx"))});
    const messages={table:{loadError:'Unable to load records',retry:'Retry',empty:'No data',loading:'Loading',total:'{total} items',page:'Page {page} / {totalPages}',prevPage:'Previous page',previousPage:'Previous page',nextPage:'Next page',actions:'Actions'}};
    const node=document.createElement('div');document.body.append(node);const root=createRoot(node);
    const tick=()=>new Promise(r=>setTimeout(r,20));
    const assert=(value,msg)=>{if(!value)throw Error(msg)};
    async function until(check,msg){for(let i=0;i<100;i++){if(check())return;await tick()}throw Error(msg)}
    const columns=[{field:'name',name:'Name',sortable:true}];const checks=[];
    function render(kind,fetcher,data){
      const view=kind==='common'
        ? React.createElement(ApiProvider,{client:{get:async(url,q)=>{const r=await fetcher(q);return {code:0,data:r.rows,meta:{total:r.total,offset:q.offset}}},resolveAsset:v=>v}},React.createElement(CommonTable,{tableOptions:{url:'/items',columns,limit:2,filter:{status:'active'},order:{name:'ASC'}}}))
        : React.createElement(AutoTable,{data,columns,fetch:fetcher,pageSize:2,pageSizeOptions:[2],filter:{status:'active'},defaultSort:{field:'name',direction:'asc'}});
      flushSync(()=>root.render(React.createElement(IntlProvider,{locale:'en',timeZone:'UTC',messages},view)));
    }
    for(const kind of ['common','auto']){
      const calls=[];
      const fetcher=async(q)=>{calls.push(structuredClone(q));if(calls.length===1||calls.length===3)throw Error('private database detail');return {rows:[{id:'current',name:'Current record'}],total:8}};
      render(kind,fetcher);
      await until(()=>node.querySelector('[role=alert]'),kind+': failure has no error state');
      assert(!node.textContent.includes('No data'),kind+': failure shown as empty');assert(!node.textContent.includes('private database detail'),kind+': internals exposed');
      [...node.querySelectorAll('button')].find(b=>b.textContent==='Retry').click();await until(()=>node.textContent.includes('Current record'),kind+': retry failed');
      assert(JSON.stringify(calls[0])===JSON.stringify(calls[1]),kind+': retry changed query');
      const next=[...node.querySelectorAll('button')].find(b=>(b.getAttribute('aria-label')||b.textContent)==='Next page');next.click();
      await until(()=>node.querySelector('[role=alert]'),kind+': page failure missing');
      [...node.querySelectorAll('button')].find(b=>b.textContent==='Retry').click();await until(()=>calls.length===4&&node.textContent.includes('Current record'),kind+': page retry failed');
      assert(JSON.stringify(calls[2])===JSON.stringify(calls[3]),kind+': retry reset page/filter/sort');assert(kind==='common'?calls[3].offset===2:calls[3].page===2,kind+': wrong retry page');
      let releaseOld;render(kind,()=>new Promise(r=>releaseOld=r));await until(()=>!!releaseOld,kind+': old query missing');render(kind,async()=>({rows:[{id:'new',name:'New scope'}],total:1}));await until(()=>node.textContent.includes('New scope'),kind+': new query missing');releaseOld({rows:[{id:'old',name:'Old scope'}],total:100});await tick();assert(node.textContent.includes('New scope')&&!node.textContent.includes('Old scope'),kind+': stale query overwrote current rows');
      render(kind,async()=>({rows:[],total:0}));await until(()=>node.textContent.includes('No data'),kind+': true empty state missing');assert(!node.querySelector('[role=alert]'),kind+': stale error in empty result');
      checks.push(kind+': failure/empty distinction',kind+': query-preserving retry',kind+': failed-page retry',kind+': stale response protection',kind+': empty recovery');
    }
    render('auto',async()=>{throw Error('remote failure')});await until(()=>node.querySelector('[role=alert]'),'remote error before local mode missing');
    let rejectRemote;render('auto',()=>new Promise((_,reject)=>rejectRemote=reject));await until(()=>!!rejectRemote,'pending remote not started');render('auto',undefined,Array.from({length:4},(_,i)=>({id:String(i),name:'Local record'})));await until(()=>node.textContent.includes('Local record'),'local mode retained remote loading/error');rejectRemote(Error('late remote failure'));await tick();assert(node.textContent.includes('Local record')&&!node.querySelector('[role=alert]'),'remote rejection replaced local data');checks.push('auto: remote failure/pending request to local data');
    const {crudTableOptions}=await import(${JSON.stringify(hookPath.replace("/auto-form/use-auto-form.ts", "/crud/resource.ts"))});
    let fallbackCalls=0,boundDeletes=0,navigated=0;const boundQueries=[];const tableRef=React.createRef();
    const boundClient={get:()=>{fallbackCalls++;throw Error('wrong request transport')},resolveAsset:v=>v};
    const resource={basePath:'/console/items',columns,getFormDef:()=>({}),filterMode:'simple',staticFilter:{owner:'current'},order:{name:'ASC'},limit:2,
      fetch:async(query)=>{boundQueries.push(structuredClone(query));return {list:boundDeletes?[]:[{id:'bound',name:'Bound record'}],total:boundDeletes?0:1,offset:query.offset}},
      deleteRecord:async(row)=>{assert(row.id==='bound','delete lost selected record');boundDeletes++},
      actionRules:{detail:{show:()=>false},edit:{disabled:()=>true},copy:{show:()=>false}}};
    const boundOptions=crudTableOptions(resource,{api:boundClient,nav:{detail:()=>navigated++,edit:()=>navigated++,copy:()=>navigated++},confirm:async()=>true,refresh:()=>tableRef.current.refresh()});
    flushSync(()=>root.render(React.createElement(IntlProvider,{locale:'en',timeZone:'UTC',messages},React.createElement(ApiProvider,{client:boundClient},React.createElement(CommonTable,{ref:tableRef,tableOptions:boundOptions,fields:['id','name']})))));
    await until(()=>node.textContent.includes('Bound record'),'bound transport did not render');assert(fallbackCalls===0,'bound list fell through to default client');
    assert(boundQueries[0].filter.owner==='current'&&boundQueries[0].order.name==='ASC'&&boundQueries[0].limit===2&&boundQueries[0].fields.join(',')==='id,name','bound list lost query projection');
    const boundRow=node.querySelector('tbody tr');boundRow.click();assert(navigated===0&&!boundRow.classList.contains('cursor-pointer'),'denied detail remains interactive');assert(node.querySelector('button[title="Edit"]').disabled,'edit state not applied');assert(!node.querySelector('button[title="Copy"]'),'hidden copy action rendered');
    node.querySelector('button[title="Delete"]').click();await until(()=>boundQueries.length===2&&node.textContent.includes('No data'),'custom delete did not refresh');assert(boundDeletes===1&&fallbackCalls===0,'delete used wrong transport');checks.push('resource: custom list query/field projection','resource: action visibility/disabled/detail','resource: custom delete and refresh');
    root.unmount();node.remove();return {pass:true,checks};
  })()`
  const result = execFileSync(
    "bunx",
    ["agent-browser", "--session", session, "eval", "--stdin"],
    { input: script, encoding: "utf8", windowsHide: true, timeout: 60000 }
  )
  if (!JSON.parse(result).pass) throw Error(result)
  console.log(result)
} finally {
  run("close")
}
