// Real-browser component fixtures; does not claim production API integration acceptance.
// IAM_CONSOLE_URL=http://127.0.0.1:4303 bun tests/auto-components-migration-browser-audit.mjs
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4303"
const session = `auto-components-migration-${process.pid}`
const componentPath = (relative) =>
  "/@fs/" +
  fileURLToPath(
    new URL(`../../../packages/ui/src/${relative}`, import.meta.url)
  )
    .replaceAll("\\", "/")
    .replace(/^\//, "")
const formPath = componentPath("components/auto-form/auto-form.tsx")
const tablePath = componentPath("components/auto-table/auto-table.tsx")
const sources = await Promise.all(
  [
    "/src/main.tsx",
    formPath,
    componentPath("components/auto-form/use-auto-form.ts"),
  ].map(async (path) => (await fetch(`${baseURL}${path}`)).text())
)
const dependency = (name) => {
  const path = [...sources.join("\n").matchAll(/"([^" ]+\.js\?v=[a-z0-9]+)"/g)]
    .map((match) => match[1])
    .find((path) => path.includes(`/${name}.js?`))
  if (!path) throw Error(`Missing Vite dependency: ${name}`)
  return JSON.stringify(path)
}
const run = (...args) =>
  execFileSync("bun", ["x", "agent-browser", "--session", session, ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 60000,
  })
try {
  run("open", baseURL)
  const script = `(async () => {
    const React=(await import(${dependency("react")})).default;
    const {createRoot}=(await import(${dependency("react-dom_client")})).default;
    const {flushSync}=(await import('/node_modules/.vite/deps/react-dom.js')).default;
    const {IntlProvider}=await import(${dependency("use-intl")});
    const {AutoForm}=await import(${JSON.stringify(formPath)});
    const {AutoTable}=await import(${JSON.stringify(tablePath)});
    const {CommonTableFilter}=await import(${JSON.stringify(componentPath("components/crud/common-table-filter.tsx"))});
    const messages=(await import(${JSON.stringify(componentPath("i18n/messages/en-US.json"))})).default;
    const h=React.createElement, node=document.createElement('section');
    document.body.append(node);const root=createRoot(node),checks=[],failures=[],evidence={};
    const tick=()=>new Promise(resolve=>setTimeout(resolve,30));
    const assert=(value,message)=>{if(!value)throw Error(message)};
    let sequence=0;
    async function render(element){const current=++sequence;flushSync(()=>root.render(h(IntlProvider,{key:current,locale:'en-US',timeZone:'UTC',messages},h('div',{'data-fixture':current},element))));await until(()=>node.querySelector('[data-fixture="'+current+'"]'),'fixture render did not commit');await tick()}
    async function check(name,fn){try{await fn();checks.push(name)}catch(error){failures.push({name,message:String(error.message)})}}
    async function until(fn,message){for(let i=0;i<80;i++){if(fn())return;await tick()}throw Error(message)}
    function fill(input,value){const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
      flushSync(()=>{setter.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}))})}
    const submit=()=>node.querySelector('form').requestSubmit();
    await check('unpaged rows and rowProps event merging',async()=>{
      let metadataClicks=0,rowClicks=0,keys=0;
      const data=Array.from({length:31},(_,i)=>({id:String(i),name:'Row '+i}));
      await render(h(AutoTable,{data,rowKey:'id',pagination:false,columns:[{field:'name',name:'Name'}],
        rowProps:row=>({'data-record-id':row.id,'data-state':'selected',className:'fixture-row',
          onClick:event=>{metadataClicks++;if(row.id==='1')event.preventDefault()},onKeyDown:()=>keys++}),
        onRowClick:()=>rowClicks++}));
      await until(()=>node.querySelectorAll('tbody tr').length===31,'rows truncated at default page size');
      assert(!node.querySelector('[aria-label="Next page"]'),'duplicate pagination visible');
      const first=node.querySelector('[data-record-id="0"]');
      assert(first.dataset.state==='selected'&&first.classList.contains('fixture-row'),'row metadata lost');
      first.click();node.querySelector('[data-record-id="1"]').click();
      first.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
      assert(metadataClicks===2&&keys===1&&rowClicks===2,'row handlers not merged or cancellation ignored');
      evidence.rows=31;
    });
    await check('native password email IDs and reveal overrides',async()=>{
      await render(h(AutoForm,{fields:{password:{name:'Password',type:'password',value:'secret',config:{id:'fixture-password'}},
        email:{name:'Email',type:'email',config:{id:'fixture-email'}},
        reveal:{name:'Reveal',type:'password',value:'visible',config:{type:'text',id:'fixture-reveal'}}}}));
      assert(node.querySelector('#fixture-password').type==='password','password exposed');
      assert(node.querySelector('#fixture-email').type==='email','email type lost');
      assert(node.querySelector('#fixture-reveal').type==='text','explicit reveal ignored');
    });
    await check('custom child required CAPTCHA blocks then submits',async()=>{
      let calls=0;
      await render(h(AutoForm,{fields:{name:{name:'Name',type:'text',value:'Valid'}},onSubmit:()=>calls++},
        h('input',{id:'fixture-captcha',required:true,pattern:'[0-9]{4}'})));
      submit();await tick();assert(calls===0,'empty CAPTCHA bypassed');
      fill(node.querySelector('#fixture-captcha'),'bad');submit();await tick();assert(calls===0,'invalid CAPTCHA bypassed');
      fill(node.querySelector('#fixture-captcha'),'1234');submit();await until(()=>calls===1,'valid CAPTCHA failed');
    });
    await check('contract numeric fraction survives native DOM submit',async()=>{
      const sent=[];
      const contract={version:'v1',operation_id:'fraction',method:'POST',path:'/fixture',schemas:{},request_body:{content:{
        'application/json':{schema:{type:'object',properties:{amount:{type:'number',minimum:0}},required:['amount'],additionalProperties:false}}}}};
      await render(h(AutoForm,{contract,fields:{amount:{name:'Amount',type:'number',value:1.5}},onSubmit:data=>sent.push(data)}));
      assert(node.querySelector('input').validity.stepMismatch,'fixture does not exercise native fractional mismatch');
      submit();await until(()=>sent.length===1,'schema-valid fraction blocked by browser');
      assert(sent[0].amount===1.5,'fraction changed in transport');
    });
    await check('formRef dependent reset preserves independent values',async()=>{
      let api;
      function Dependent(){const ref=React.useRef(null),previous=React.useRef('a');
        const onChange=React.useCallback(values=>{if(values.tenant!==previous.current){previous.current=values.tenant;ref.current.setValue('permissions',[])}},[]);
        React.useEffect(()=>{api=ref});
        return h(AutoForm,{formRef:ref,onChange,fields:{tenant:{name:'Tenant',type:'text',value:'a'},
          label:{name:'Label',type:'text',value:'Keep me'},permissions:{name:'Permissions',type:'text',value:['read']}}})}
      await render(h(Dependent));await tick();
      fill(node.querySelector('input[name="tenant"]'),'b');await tick();
      assert(api.current.getValues().tenant==='b','tenant change lost');
      assert(api.current.getValues().label==='Keep me','dependent reset erased label');
      assert(Array.isArray(api.current.getValues().permissions)&&api.current.getValues().permissions.length===0,'dependent permissions not reset');
    });
    await check('custom repeated inputs enforce each native constraint',async()=>{
      let calls=0;
      function Repeated({value,onChange}){const values=value??['',''];return h('div',null,...values.map((entry,index)=>
        h('input',{key:index,'data-repeat':index,type:'email',required:true,value:entry,
          onChange:event=>onChange(values.map((old,i)=>i===index?event.target.value:old))})))}
      await render(h(AutoForm,{fields:{recipients:{name:'Recipients',type:'custom',value:['first@example.test',''],component:Repeated}},onSubmit:()=>calls++}));
      submit();await tick();assert(calls===0,'empty repeated input bypassed');
      fill(node.querySelector('[data-repeat="1"]'),'invalid');submit();await tick();assert(calls===0,'malformed repeated email bypassed');
      fill(node.querySelector('[data-repeat="1"]'),'second@example.test');submit();await until(()=>calls===1,'valid repeated inputs failed');
    });
    await check('legacy numeric initial value is native step base',async()=>{
      let calls=0;
      await render(h(AutoForm,{mode:'edit',initialData:{amount:0.5},fields:{amount:{name:'Amount',type:'number',value:0}},onSubmit:()=>calls++}));
      const input=node.querySelector('input');
      evidence.numericInitial={value:input.value,defaultValue:input.defaultValue,stepMismatch:input.validity.stepMismatch};
      assert(!input.validity.stepMismatch,'browser rejects fixture initial step base');
      submit();await tick();assert(calls===1,'hook rejects native-valid edit prefill');
    });
    await check('workspace sticky geometry and effective toolbar actions',async()=>{
      let refreshed=0,imported=0,created=0,blob;
      const data=Array.from({length:80},(_,i)=>({id:String(i),name:'Row '+i,secret:'DO_NOT_EXPORT',detail:'Detail '+i}));
      node.style.cssText='position:fixed;inset:0 auto auto 0;width:800px;height:480px;z-index:9999;background:white';
      await render(h('div',{style:{height:'100%',display:'flex',minHeight:0,minWidth:0,width:'100%'}},h(AutoTable,{
        data,rowKey:'id',fill:true,pageSize:50,searchable:true,
        onRefresh:()=>{refreshed++},onImport:()=>{imported++},
        toolbar:h('button',{onClick:()=>created++},'Create fixture'),
        columns:[{field:'name',name:'Name',width:350,render:value=>'Rendered '+value},
          {field:'detail',name:'Detail',width:500},
          {field:'action',name:'Actions',type:'actions',width:140,render:()=>h('button',null,'Edit')}]
      })));
      node.querySelector('[data-fixture]').style.cssText='display:flex;height:100%;min-height:0;min-width:0;width:100%';
      await tick();
      const table=node.querySelector('table'),viewport=table.parentElement,footer=node.querySelector('[data-slot="table-footer"]');
      const sequence=()=>node.querySelector('tbody [data-column="sequence"]');
      const action=()=>node.querySelector('tbody [data-column="actions"]');
      const middle=()=>node.querySelector('tbody tr td:nth-child(2)');
      const rect=el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,height:r.height}};
      const before={sequence:rect(sequence()),action:rect(action()),middle:rect(middle()),header:rect(table.querySelector('th')),footer:rect(footer)};
      assert(viewport.scrollWidth>viewport.clientWidth+100,'missing horizontal overflow');
      assert(viewport.scrollHeight>viewport.clientHeight+100,'missing vertical overflow');
      assert(Math.abs(before.footer.bottom-node.getBoundingClientRect().bottom)<3,'footer does not fill to bottom');
      assert(Math.abs(viewport.getBoundingClientRect().bottom-before.footer.top)<3,'viewport leaves unused height');
      viewport.scrollLeft=240;await tick();
      assert(Math.abs(rect(sequence()).left-before.sequence.left)<2,'sequence moved horizontally');
      assert(Math.abs(rect(action()).right-before.action.right)<2,'actions moved horizontally');
      assert(rect(middle()).left<before.middle.left-100,'middle columns did not scroll');
      viewport.scrollTop=300;await tick();
      assert(Math.abs(rect(table.querySelector('th')).top-before.header.top)<2,'header moved vertically');
      assert(Math.abs(rect(footer).top-before.footer.top)<2,'footer moved with rows');
      node.querySelector('[aria-label="Next page"]').click();await tick();
      assert(sequence().textContent.trim()==='51','second-page sequence offset incorrect');
      assert(viewport.scrollTop===0,'page change left new rows outside viewport');
      fill(node.querySelector('input[type="search"]'),'Row 7');await tick();
      assert(sequence().textContent.trim()==='1'&&node.querySelectorAll('tbody tr').length===11,'search failed or page not reset');
      const button=text=>Array.from(node.querySelectorAll('button')).find(el=>el.textContent.trim()===text);
      button('Refresh').click();await tick();assert(refreshed===1,'refresh not invoked');
      assert(node.querySelector('input[type="search"]').value==='Row 7','refresh cleared search');
      button('Import').click();await tick();assert(imported===1,'import callback not invoked');
      button('Create fixture').click();assert(created===1,'custom create toolbar lost');
      const oldURL=URL.createObjectURL,oldClick=HTMLAnchorElement.prototype.click;
      try{URL.createObjectURL=value=>{blob=value;return 'blob:fixture'};HTMLAnchorElement.prototype.click=function(){};
        button('Export current page').click();await tick();
        const csv=await blob.text();assert(csv.includes('Rendered Row 7')&&csv.includes('Detail 7'),'export missed displayed renderer');
        assert(!csv.includes('DO_NOT_EXPORT')&&!csv.includes('Actions')&&!csv.includes('Edit'),'export leaked hidden/action fields');
        evidence.exportRows=csv.split('\\r\\n').length-1;
      }finally{URL.createObjectURL=oldURL;HTMLAnchorElement.prototype.click=oldClick}
      evidence.workspace={before,after:{sequence:rect(sequence()),action:rect(action()),footer:rect(footer)},refresh:refreshed,import:imported,create:created};
    });
    await check('local shrinking pages and failed refresh preserve usable rows',async()=>{
      let shrink;
      function Shrinking(){const [data,setData]=React.useState(Array.from({length:25},(_,i)=>({id:String(i),name:'Record '+i})));
        shrink=()=>setData(data.slice(0,3));return h(AutoTable,{data,rowKey:'id',pageSize:10,
          onRefresh:async()=>{throw Error('fixture read failed')},columns:[{field:'name',name:'Name'}]})}
      await render(h(Shrinking));
      node.querySelector('[aria-label="Next page"]').click();await tick();
      assert(node.querySelector('tbody [data-column="sequence"]').textContent==='11','fixture failed to reach second page');
      flushSync(()=>shrink());await tick();await tick();
      assert(node.querySelectorAll('tbody tr').length===3,'shrunk collection stranded on empty page');
      assert(node.querySelector('tbody [data-column="sequence"]').textContent==='1','page was not clamped');
      Array.from(node.querySelectorAll('button')).find(el=>el.textContent.trim()==='Refresh').click();await tick();
      assert(node.querySelector('[role="alert"]'),'failed refresh silently ignored');
      assert(node.querySelectorAll('tbody tr').length===3,'failed refresh erased existing rows');
    });
    await check('split refresh countdown reset latest callback locking off and cleanup',async()=>{
      const realNow=Date.now,realInterval=window.setInterval,realClear=window.clearInterval;
      let now=realNow(),nextTimer=900000,timers=new Map(),calls=[],update,release;
      Date.now=()=>now;
      window.setInterval=(callback,delay,...args)=>{
        if(delay!==1000)return realInterval(callback,delay,...args);
        const id=nextTimer++;timers.set(id,callback);return id;
      };
      window.clearInterval=id=>{if(timers.has(id))timers.delete(id);else realClear(id)};
      async function advance(seconds){for(let i=0;i<seconds;i++){now+=1000;flushSync(()=>{for(const callback of [...timers.values()])callback()});await tick()}}
      const refresh=()=>node.querySelector('[data-slot="refresh-button"] button[aria-label="Refresh"]');
      async function choose(label){node.querySelector('[aria-label="Auto refresh"]').click();await tick();
        const items=Array.from(document.querySelectorAll('[role="menuitemradio"]'));
        assert(items.length===6,'missing off or interval choices');
        const item=items.find(el=>el.textContent.trim()===label);assert(item,'missing '+label);item.click();await tick()}
      try{
        function Fixture(){const [config,setConfig]=React.useState({version:0,slow:false,loading:false});update=setConfig;
          return h(AutoTable,{data:[{id:'one',name:'Record'}],rowKey:'id',loading:config.loading,
            columns:[{field:'name',name:'Name'}],onRefresh:async()=>{calls.push(config.version);if(config.slow)await new Promise(resolve=>{release=resolve})}})}
        await render(h(React.StrictMode,null,h(Fixture)));
        await advance(6);assert(calls.length===0,'auto refresh enabled by default');
        await choose('Every 5 seconds');await advance(4);
        assert(calls.length===0&&refresh().textContent.trim()==='1s','countdown fired early');
        refresh().click();await tick();assert(calls.length===1&&refresh().textContent.trim()==='5s','manual refresh did not reset countdown');
        await advance(4);assert(calls.length===1,'manual refresh kept old deadline');
        await advance(1);assert(calls.length===2,'timer missing or StrictMode double refresh');
        await advance(2);flushSync(()=>update({version:1,slow:false,loading:false}));await tick();
        await advance(3);assert(calls.length===3&&calls[2]===1,'callback stale or rerender restarted countdown');
        flushSync(()=>update({version:2,slow:true,loading:false}));await tick();await advance(5);
        assert(calls.length===4&&refresh().disabled,'pending refresh not locked');
        await advance(10);assert(calls.length===4,'overlapping scheduled reads');
        release();await tick();
        flushSync(()=>update({version:3,slow:false,loading:true}));await tick();await advance(5);
        assert(calls.length===4,'read started during external loading');
        flushSync(()=>update({version:3,slow:false,loading:false}));await tick();
        await choose('Off');await advance(10);assert(calls.length===4&&timers.size===0,'off did not stop timer');
        await choose('Every 10 seconds');assert(timers.size===1,'timer did not restart');
        await render(h('div',null,'Unmounted'));await advance(20);
        assert(calls.length===4&&timers.size===0,'unmounted refresh still polling');
        await render(h(CommonTableFilter,{filterOptions:[],onChange:()=>{},onRefresh:()=>calls.push('common')}));
        await choose('Every 5 seconds');await advance(5);
        assert(calls.length===5&&calls[4]==='common','CommonTableFilter did not share scheduled refresh');
        await render(h('div',null,'Unmounted'));assert(timers.size===0,'CommonTableFilter leaked timer');
        evidence.autoRefresh={calls,defaultOff:true,manualReset:true,latestCallback:true,overlapPrevented:true,offAndCleanup:true};
      }finally{Date.now=realNow;window.setInterval=realInterval;window.clearInterval=realClear}
    });
    await check('numeric pagination highlights jumps and page-size changes',async()=>{
      await render(h(AutoTable,{data:Array.from({length:240},(_,i)=>({id:String(i),name:'Record '+i})),rowKey:'id',pageSize:20,
        columns:[{field:'name',name:'Name'}]}));
      assert(node.querySelector('[aria-label="Page 12"]')&&node.querySelector('[data-slot="pagination-ellipsis"]'),'numbered range or ellipsis missing');
      node.querySelector('[aria-label="Page 12"]').click();await tick();
      assert(node.querySelector('tbody [data-column="sequence"]').textContent==='221','numbered jump loaded wrong rows');
      assert(node.querySelector('[aria-label="Page 12"][aria-current="page"]'),'active page missing');
      assert(node.querySelector('[aria-label="Next page"]').disabled,'last page next enabled');
      const select=node.querySelector('select');select.value='100';select.dispatchEvent(new Event('change',{bubbles:true}));await tick();
      assert(node.querySelector('[aria-label="Page 3"]')&&!node.querySelector('[aria-label="Page 4"]'),'page size did not recompute numbered pages');
      assert(!node.querySelector('[data-slot="pagination-ellipsis"]'),'ellipsis shown for three pages');
      assert(node.querySelector('[aria-label="Page 1"][aria-current="page"]'),'page size did not reset active page');
      evidence.pagination={rows:240,pages:12,jumpSequence:221,pageSize100Pages:3};
    });
    root.unmount();node.remove();return {pass:failures.length===0,checks,failures,evidence,scope:'component fixtures; no backend API acceptance'};
  })()`
  const result = execFileSync(
    "bun",
    ["x", "agent-browser", "--session", session, "eval", "--stdin"],
    {
      input: script,
      encoding: "utf8",
      windowsHide: true,
      timeout: 60000,
    }
  )
  console.log(result)
  if (!JSON.parse(result).pass) process.exitCode = 1
} finally {
  run("close")
}
