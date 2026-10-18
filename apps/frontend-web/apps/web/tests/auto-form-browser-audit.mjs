// Run against the Web Vite dev server. Tests real client hooks, not API integration.
// IAM_CONSOLE_URL=http://127.0.0.1:4303 bun tests/auto-form-browser-audit.mjs
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4303"
const session = `auto-form-${process.pid}`
const hookPath =
  "/@fs/" +
  fileURLToPath(
    new URL(
      "../../../packages/ui/src/components/auto-form/use-auto-form.ts",
      import.meta.url
    )
  )
    .replaceAll("\\", "/")
    .replace(/^\//, "")
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
  execFileSync("bun", ["x", "agent-browser", "--session", session, ...args], {
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
    const {useAutoForm} = await import(${JSON.stringify(hookPath)});
let api, resolveRule, calls=0, submits=0;
const fields={name:{type:'text',value:'initial',config:{rules:[()=>{calls++;return new Promise(r=>resolveRule=r)}]}}};
const node=document.createElement('div'); document.body.append(node); const root=createRoot(node);
function Probe(props){api=useAutoForm({...props, fields, onSubmit:()=>{submits++}});return React.createElement('output',null,JSON.stringify({values:api.values,errors:api.errors}));}
function render(readonly){flushSync(()=>root.render(React.createElement(IntlProvider,{locale:'en',timeZone:'UTC',messages:{form:{required:'Required'}}},React.createElement(Probe,{readonly}))));}
const tick=()=>new Promise(r=>setTimeout(r,20));
const assert=(v,m)=>{if(!v)throw Error(m)};
render(false); await tick(); const oldSetValue=api.setValue; const pending=api.validateField('name'); assert(calls===1,'editable field validation missing');
render(true); await tick(); resolveRule('invalid'); await pending; await tick(); assert(!api.errors.name,'readonly async error write');
await api.validateField('name'); assert(calls===1,'readonly invokes rule'); oldSetValue('name','stale'); api.setValue('name','changed');await tick();assert(api.values.name==='initial','readonly write'); await api.handleSubmit();assert(submits===0,'readonly submit');
render(false);await tick(); const submit=api.handleSubmit(); const duplicate=api.handleSubmit(); assert(calls===2,'duplicate validation'); render(true); await tick();resolveRule('invalid');await Promise.all([submit,duplicate]);await tick(); assert(!api.errors.name && submits===0,'readonly transition submit');
render(false); await tick();const success=api.handleSubmit();resolveRule(undefined);await success;await tick();assert(submits===1,'editable submit after lock release');

let contractApi, releaseContract, contractSubmits=0, submittedAmount;
const contract={version:'v1',operation_id:'create',method:'POST',path:'/items',schemas:{},request_body:{content:{'application/json':{schema:{type:'object',properties:{amount:{type:'integer',minimum:1}},required:['amount'],additionalProperties:false}}}}};
const contractFields={amount:{type:'number',name:'Amount',value:'2500',config:{rules:[()=>new Promise(r=>releaseContract=r)]}}};
function ContractProbe({metadata}){contractApi=useAutoForm({fields:contractFields,contract:metadata,onSubmit:(data)=>{contractSubmits++;submittedAmount=data.amount}});return React.createElement('output',null,JSON.stringify(contractApi.errors));}
function renderContract(metadata){flushSync(()=>root.render(React.createElement(IntlProvider,{locale:'en',timeZone:'UTC',messages:{form:{required:'Required',contract:{loading:'Rules unavailable',unsupported:'Unsupported rule'}}}},React.createElement(ContractProbe,{metadata}))));}
renderContract(contract);await tick();const oldContractSubmit=contractApi.handleSubmit;const stale=contractApi.handleSubmit();await tick();renderContract(null);await tick();releaseContract(undefined);await stale;await tick();assert(contractSubmits===0,'stale contract submitted after invalidation');
const staleClosure=oldContractSubmit();await tick();if(releaseContract)releaseContract(undefined);await staleClosure;assert(contractSubmits===0,'old submit callback bypassed contract invalidation');
renderContract(contract);await tick();const superseded=contractApi.handleSubmit();await tick();renderContract(null);await tick();renderContract(contract);await tick();releaseContract(undefined);await superseded;await tick();assert(contractSubmits===0,'restored contract revived invalidated submission');
renderContract(contract);await tick();const accepted=contractApi.handleSubmit();await tick();releaseContract(undefined);await accepted;await tick();assert(contractSubmits===1 && submittedAmount===2500 && contractApi.values.amount==='2500','contract parsing changed editing values or failed to submit');
const {CrudForm}=await import(${JSON.stringify(hookPath.replace("/auto-form/use-auto-form.ts", "/crud/crud-form.tsx"))});
const crudMessages={crud:{loading:'Loading',loadError:'Unable to load',retry:'Retry',success:'Saved',submit:'Save',edit:'Edit'},form:{required:'Required',submitFailed:'Could not submit',contract:{loading:'Rules unavailable',unsupported:'Unsupported rule',maxLength:'Too long'}}};
let adds=[],edits=[];
const crudFactory={getFormDef:()=>({title:{type:'text',name:'Title'},unique:{type:'text',name:'Unique',copy:false,value:'new-key'}}),getInfo:async(id)=>({title:id,unique:'source-key'}),commitAdd:async(body)=>{adds.push(body)},commitEdit:async(id,body)=>{edits.push({id,body})}};
function renderCrud(props){flushSync(()=>root.render(React.createElement(IntlProvider,{locale:'en',timeZone:'UTC',messages:crudMessages},React.createElement(CrudForm,{factory:crudFactory,formId:'crud-regression',...props}))));}
async function until(check,message){for(let i=0;i<100;i++){if(check())return;await tick()}throw Error(message)}
function submitCrud(){node.querySelector('form').requestSubmit()}
renderCrud({mode:'copy',id:'source'});await until(()=>node.querySelector('input')?.value==='source','copy did not load');submitCrud();await until(()=>adds.length+edits.length===1,'copy did not submit');assert(adds.length===1&&edits.length===0,'copy edited source instead of creating');assert(adds[0].unique==='new-key','copy retained excluded unique field');
renderCrud({mode:'add',copyId:'second'});await until(()=>node.querySelector('input')?.value==='second','add+copyId did not reload');submitCrud();await until(()=>adds.length===2,'add+copyId failed to create');assert(edits.length===0,'copy alias edited source');
const lockedFactory={...crudFactory,editable:false};renderCrud({mode:'edit',id:'locked',factory:lockedFactory});await until(()=>node.querySelector('form')&&!node.querySelector('input'),'readonly did not render readonly values');submitCrud();await tick();assert(edits.length===0,'readonly submitted edit');
let readAttempts=0;const failedFactory={...crudFactory,getInfo:async()=>{if(++readAttempts===1)throw Error('private backend detail');return {title:'recovered'}}};
renderCrud({mode:'edit',id:'failure',factory:failedFactory});await until(()=>node.querySelector('[role=alert]'),'read error not visible');assert(!node.querySelector('form'),'read error presented writable blank form');assert(!node.textContent.includes('private backend detail'),'read error leaked internals');node.querySelector('button').click();await until(()=>node.querySelector('input')?.value==='recovered','retry did not recover');submitCrud();await until(()=>edits.length===1,'recovered edit failed');assert(edits[0].id==='failure','retry edited wrong resource');
let resolveOld;const slowFactory={...crudFactory,getInfo:()=>new Promise(r=>resolveOld=r)};renderCrud({mode:'edit',id:'same',factory:slowFactory});await tick();renderCrud({mode:'edit',id:'same',factory:{...crudFactory,getInfo:async()=>({title:'new-scope'})}});await until(()=>node.querySelector('input')?.value==='new-scope','factory scope did not reload');resolveOld({title:'old-scope'});await tick();assert(node.querySelector('input')?.value==='new-scope','stale factory response overwrote current scope');
const invalidContract={"version": "v1", "operation_id": "edit", "method": "PATCH", "path": "/items/id", "schemas": {}, "request_body": {"content": {"application/json": {"schema": {"type": "object", "properties": {"title": {"type": "string", "maxLength": 2}, "unique": {"type": "string"}}}}}}};
renderCrud({mode:'edit',id:'too-long',factory:{...crudFactory,contract:invalidContract}});await until(()=>node.querySelector('input')?.value==='too-long','contract form did not load');submitCrud();await tick();assert(edits.length===1,'CrudForm bypassed operation contract');
let releaseOldValidation;const pendingFactory={...crudFactory,getFormDef:()=>({title:{type:'text',name:'Title',config:{rules:[()=>new Promise(r=>releaseOldValidation=r)]}}})};
renderCrud({mode:'edit',id:'old-write',factory:pendingFactory});await until(()=>node.querySelector('input')?.value==='old-write','pending form did not load');submitCrud();await until(()=>!!releaseOldValidation,'async validation not started');renderCrud({mode:'edit',id:'new-write',factory:crudFactory});await until(()=>node.querySelector('input')?.value==='new-write','replacement did not load');releaseOldValidation(undefined);await tick();assert(edits.length===1,'obsolete form submitted after record switch');
let releaseCommit,staleNotifications=0;const committingFactory={...crudFactory,commitEdit:()=>new Promise(r=>releaseCommit=r),commitCallback:{onSuccess:()=>staleNotifications++}};
renderCrud({mode:'edit',id:'in-flight',factory:committingFactory,onSubmitted:()=>staleNotifications++});await until(()=>node.querySelector('input')?.value==='in-flight','in-flight form did not load');submitCrud();await until(()=>!!releaseCommit,'commit not started');renderCrud({mode:'edit',id:'current',factory:crudFactory});await until(()=>node.querySelector('input')?.value==='current','current form not loaded');assert(!node.querySelector('button[type=submit]').disabled,'previous commit blocked current form');releaseCommit();await tick();assert(staleNotifications===0,'obsolete commit triggered current navigation');
renderCrud({mode:'edit'});await tick();assert(!node.querySelector('form'),'missing edit id presented blank form');

const issue={in:'body',path:'/title',code:'unique',message:'Title is already used'};
let failedCallbacks=0;
const writeFailure={...crudFactory,commitEdit:async()=>{throw {validationErrors:[issue,{...issue,path:'/hidden',message:'Unbound issue'}]}},commitCallback:{onError:()=>failedCallbacks++}};
renderCrud({mode:'edit',id:'duplicate',factory:writeFailure});await until(()=>node.querySelector('input')?.value==='duplicate','failed commit form not loaded');submitCrud();await until(()=>node.textContent.includes('Title is already used'),'structured field error missing');assert(node.querySelector('input').getAttribute('aria-invalid')==='true','field not marked invalid');assert(node.querySelector('[role=alert]')?.textContent.includes('Unbound issue'),'unbound issue lost');assert(failedCallbacks===1,'error callback count incorrect');node.querySelector('input').dispatchEvent(new FocusEvent('focusout',{bubbles:true}));await tick();assert(node.textContent.includes('Title is already used'),'blur cleared unresolved server error');
const genericFailure={...crudFactory,commitEdit:async()=>{throw Error('private stack detail')}};
renderCrud({mode:'edit',id:'generic',factory:genericFailure});await until(()=>node.querySelector('input')?.value==='generic','generic failure form not loaded');submitCrud();await until(()=>node.querySelector('[role=alert]'),'generic failure swallowed');assert(node.querySelector('[role=alert]').textContent==='Could not submit','translated fallback missing');assert(!node.textContent.includes('private stack detail'),'private exception exposed');
let rejectPending;
const pendingErrorFactory={...crudFactory,commitEdit:()=>new Promise((_,reject)=>rejectPending=reject)};
renderCrud({mode:'edit',id:'pending-error',factory:pendingErrorFactory});await until(()=>node.querySelector('input')?.value==='pending-error','pending error form not loaded');submitCrud();await until(()=>!!rejectPending,'pending request not started');
const input=node.querySelector('input');const nativeSetter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;flushSync(()=>{nativeSetter.call(input,'corrected');input.dispatchEvent(new Event('input',{bubbles:true}))});rejectPending({validationErrors:[issue]});await tick();assert(!node.textContent.includes('Title is already used'),'old payload error attached to corrected input');assert(input.value==='corrected','pending rejection changed input');

let presenceSubmissions=[];
const presenceContract={...invalidContract,method:'POST',request_body:{content:{'application/json':{schema:{type:'object',properties:{title:{type:'string'}},required:['title'],additionalProperties:false}}}}};
const presenceFactory={...crudFactory,contract:presenceContract,getFormDef:()=>({title:{type:'text',name:'Title',value:''}}),commitAdd:async body=>{presenceSubmissions.push(body)}};
renderCrud({mode:'add',factory:presenceFactory});await tick();assert(node.textContent.includes('*'),'schema required indicator disappeared');node.querySelector('form').requestSubmit();await until(()=>presenceSubmissions.length===1,'native form submission rejected schema-valid empty string');assert(presenceSubmissions[0].title==='','empty string changed in transport');
let localizedApi, localizedWrites=0;
let customLocaleError, delayLocaleRule=false, releaseLocaleRule;
const localizedFields={name:{type:'text',name:'Name',value:'x',config:{rules:[()=>delayLocaleRule ? new Promise(resolve=>releaseLocaleRule=resolve) : customLocaleError]}}};
const localContract=(locale)=>({version:locale,locale,operation_id:'localized',method:'POST',path:'/localized',schemas:{},messages:{minLength:{code:'min',message_key:'min',template:locale+' minimum {min}'}},issue_messages:{unique:{code:'unique',message_key:'unique',template:locale+' conflict {limit}'}},request_body:{content:{'application/json':{schema:{type:'object',properties:{name:{type:'string',minLength:3}}}}}}});
function LocalizedProbe({metadata}){localizedApi=useAutoForm({fields:localizedFields,contract:metadata,onSubmit:async()=>{localizedWrites++;throw {validationErrors:[{in:'body',path:'/name',code:'unique',message_key:'unique',params:{limit:3},message:'original conflict'},{in:'header',code:'unique',message_key:'unique',params:{limit:3},message:'original global'}]}}});return React.createElement('output',null,JSON.stringify(localizedApi.errors));}
function renderLocalized(locale,metadata=localContract(locale)){flushSync(()=>root.render(React.createElement(IntlProvider,{locale,timeZone:'UTC',messages:{form:{invalid:'Invalid',submitFailed:locale+' failed',contract:{loading:'Loading'}}}},React.createElement(LocalizedProbe,{metadata}))));}
renderLocalized('en-US');await tick();await localizedApi.validateField('name');await tick();assert(localizedApi.errors.name==='en-US minimum 3','initial client error');
renderLocalized('zh-CN',null);await tick();assert(localizedApi.values.name==='x','metadata loading reset input');
renderLocalized('zh-CN');await until(()=>localizedApi.errors.name==='zh-CN minimum 3','client error did not retranslate');
renderLocalized('ms-MY');await until(()=>localizedApi.errors.name==='ms-MY minimum 3','Malay client error did not retranslate');assert(localizedWrites===0,'language change submitted form');
flushSync(()=>localizedApi.setValue('name','valid'));await until(()=>localizedApi.values.name==='valid','valid input did not render');await localizedApi.handleSubmit();await until(()=>localizedApi.errors.name==='ms-MY conflict 3','server error not translated').catch(e=>{throw Error(e.message+' '+JSON.stringify({errors:localizedApi.errors,values:localizedApi.values,writes:localizedWrites}))});assert(localizedApi.errors._form==='ms-MY conflict 3','global server error missing');
renderLocalized('zh-CN');await until(()=>localizedApi.errors.name==='zh-CN conflict 3','server error did not retranslate');assert(localizedApi.values.name==='valid'&&localizedWrites===1,'translation reset or resubmitted');assert(localizedApi.errors._form==='zh-CN conflict 3','global server error not translated');
flushSync(()=>localizedApi.setValue('name','corrected'));renderLocalized('en-US');await tick();assert(!localizedApi.errors.name,'language change resurrected cleared field error');assert(localizedApi.values.name==='corrected','corrected value lost');
customLocaleError='old custom error';await localizedApi.validateField('name');await until(()=>localizedApi.errors.name==='old custom error','custom error missing');delayLocaleRule=true;renderLocalized('ms-MY');await until(()=>!!releaseLocaleRule,'language revalidation did not start');flushSync(()=>localizedApi.setValue('name','newer input'));await until(()=>localizedApi.values.name==='newer input','newer input not rendered');releaseLocaleRule('obsolete language error');await tick();assert(!localizedApi.errors.name,'late language revalidation overwrote newer input');
root.unmount();node.remove();return {pass:true,calls,submits,checks:['readonly validation transition','pending errors discarded','readonly setValue','readonly submit','duplicate submit','pending submit transition','editable restoration','pending contract invalidation','stale submit callback','contract invalidation and restoration','contract parsed numeric submit','copy creates and resets unique fields','copy alias creates','factory readonly blocks submit','read error and retry','factory scope race','CRUD contract validation','missing edit ID','pending validation invalidated by record switch','obsolete commit feedback suppressed','structured server field errors','unbound errors visible globally','generic submission failure visible without leaking internals','stale errors after input changes discarded','JSON Schema required uses presence with native form submission','three-language client and server errors preserve inputs and never resubmit','cleared server errors do not reappear','late language validation cannot overwrite newer input']};
})()`
  const result = execFileSync(
    "bun",
    ["x", "agent-browser", "--session", session, "eval", "--stdin"],
    { input: script, encoding: "utf8", windowsHide: true, timeout: 60000 }
  )
  if (!JSON.parse(result).pass) throw new Error(result)
  console.log(result)
} finally {
  run("close")
}
