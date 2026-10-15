#!/usr/bin/env python3
"""Run the prototype's browser assertions against an already running local server."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', default='http://127.0.0.1:4173')
parser.add_argument('--width', type=int, default=1440)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--session', default=f'prototype-audit-{uuid.uuid4().hex[:10]}')
parser.add_argument('--capture', action='store_true', help='Capture each passed suite screenshot and current accessibility audit')
parser.add_argument('suites', nargs='*', help='Optional browser-*.js basenames; default: all suites')
args = parser.parse_args()
paths = sorted((ROOT / 'tests').glob('browser-*.js'))
if args.suites:
    unknown = set(args.suites) - {path.name for path in paths}
    if unknown:
        parser.error(f'Unknown suites: {sorted(unknown)}')
    paths = [path for path in paths if path.name in args.suites]
if args.width < 320:
    parser.error('Viewport width must be at least 320')
base = ['bunx', 'agent-browser', '--session', args.session]

class BrowserSessionUnsafe(RuntimeError):
    """The next suite must not reuse a possibly still executing page."""

def client(command, source=None):
    result = subprocess.run(base + command, input=source, text=True, capture_output=True, cwd=ROOT, timeout=180)
    if result.returncode:
        raise RuntimeError((result.stdout + result.stderr).strip())
    return result.stdout.strip()

def run(command, source=None):
    if command != ['eval', '--stdin'] or source is None:
        return client(command, source)
    # The CLI retries its entire Runtime.evaluate request after a 30s socket
    # read timeout. Launch once under a stable key; polls never replay actions.
    job_key = uuid.uuid4().hex
    launch = """(()=>{const key=KEY;const jobs=window.__prototypeBrowserJobs??={};if(jobs[key])return {started:false};const job=jobs[key]={status:'pending',runs:1};Promise.resolve().then(()=>(0,eval)(SOURCE)).then(value=>{job.value=value;job.status='passed'},error=>{job.error=String(error?.stack||error);job.status='failed'});return {started:true}})()""".replace('KEY', json.dumps(job_key)).replace('SOURCE', json.dumps(source))
    def recover(reason):
        # A failed transport does not cancel Runtime.evaluate or its Promise.
        # Only a confirmed close makes it safe to run another suite.
        try:
            client(['close'])
            client(['open', args.url.rstrip('/') + '/#/overview'])
            client(['set', 'viewport', str(args.width), '812' if args.width < 700 else '1000'])
        except (RuntimeError, subprocess.TimeoutExpired) as error:
            raise BrowserSessionUnsafe(f'{reason}; owned browser recovery failed: {error}') from error
        raise RuntimeError(f'{reason}; owned browser closed and viewport restored')

    try:
        client(command, launch)
    except (RuntimeError, subprocess.TimeoutExpired) as error:
        recover(f'Browser script launch transport failed: {error}')
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        try:
            outcome = json.loads(client(command, "(()=>window.__prototypeBrowserJobs?.[" + json.dumps(job_key) + "]||{status:'missing'})()"))
            if not isinstance(outcome, dict):
                raise ValueError('Browser job poll must return an object')
        except (RuntimeError, ValueError, subprocess.TimeoutExpired) as error:
            recover(f'Browser script polling failed: {error}')
        if outcome.get('status') == 'passed':
            if outcome.get('runs') != 1:
                raise RuntimeError('Browser script was executed more than once')
            return json.dumps(outcome.get('value'), ensure_ascii=False)
        if outcome.get('status') == 'failed':
            raise RuntimeError(outcome.get('error', 'Browser script failed'))
        if outcome.get('status') == 'missing':
            recover('Browser execution context disappeared before its result was read')
        time.sleep(0.25)
    # Stop this owned browser explicitly: a Python timeout does not cancel the
    # page's asynchronous script, which could otherwise outlive the next reset.
    recover('Browser script exceeded its 180s deadline')

def decoded(output):
    data = json.loads(output)
    # agent-browser quotes JS string results; some older suites stringify their result.
    if isinstance(data, str):
        data = json.loads(data)
    if not isinstance(data, dict):
        raise ValueError('Suite must return a structured assertion result')
    return data

def fingerprints():
    return {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted((ROOT / 'src').glob('*')) if path.is_file()}

before = fingerprints()
report = {'url': args.url, 'width': args.width, 'session': args.session, 'sourceHashes': before, 'suites': []}
try:
    run(['open', args.url.rstrip('/') + '/#/overview'])
    run(['set', 'viewport', str(args.width), '812' if args.width < 700 else '1000'])
    for path in paths:
        try:
            # These are foreground interaction tests. A remembered active tab
            # can still be hidden after another owned session takes focus.
            run(['tab', 't1'])
            foreground = decoded(run(['eval', '--stdin'], "(()=>({pass:document.visibilityState==='visible'&&document.hasFocus(),visibility:document.visibilityState,focused:document.hasFocus()}))()"))
            if not foreground.get('pass'):
                raise RuntimeError('Foreground test tab is not visible and focused: ' + json.dumps(foreground))
            run(['eval', '--stdin'], "localStorage.removeItem('collaboration-prototype-v1');sessionStorage.clear();location.hash='/overview';")
            run(['reload'])
            seed = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const {read}=await import('./src/store.js');console.log(JSON.stringify(read()));"], cwd=ROOT, text=True, capture_output=True, check=True)
            run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(seed.stdout.strip()) + ");")
            run(['reload'])
            if path.name in {'browser-previews.js', 'browser-preview-resize.js', 'browser-features.js'}:
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}}; const {read}=await import('./src/store.js'); const s=structuredClone(read()); s.work.find(w=>w.id==='W-104').previewResources=" + json.dumps([
                    {'id':'primary','name':'Web · 主预览','kind':'Web','status':'就绪'},
                    {'id':'secondary','name':'Web · 管理端','kind':'Web','status':'就绪'},
                    {'id':'api','name':'API','kind':'API','status':'就绪'},
                    {'id':'terminal','name':'终端','kind':'终端','status':'就绪'},
                    {'id':'files','name':'仓库代码','kind':'仓库','status':'就绪'},
                    {'id':'readme','name':'README.md','kind':'文稿','status':'就绪'},
                ], ensure_ascii=False) + ";s.executions.find(e=>e.id==='EX-demo-W-104').workSnapshot.previewResources=structuredClone(s.work.find(w=>w.id==='W-104').previewResources);const {createExecutionOn,executionCommandOn}=await import('./src/executions.js');for(const workId of ['W-103','W-201']){const w=s.work.find(w=>w.id===workId);w.team={executorIds:['H-lin'],checkerIds:[]};const e=createExecutionOn(s,{tenantId:s.activeTenant,kind:'INTERACTIVE',projectId:w.project,workId,nodeId:'n1',runtime:'Codex CLI'});e.id='EX-demo-'+workId;e.workSnapshot.previewResources=[{id:'primary',name:'本地预览样例',kind:w.type==='软件'?'Web':'文稿',status:'就绪',mode:'LOCAL_SIMULATION'}];executionCommandOn(s,{tenantId:s.activeTenant,executionId:e.id,revision:e.revision,action:'start',reason:'browser fixture explicitly simulates runtime acceptance'});}console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name in {'browser-channel-management.js', 'browser-project-metadata.js', 'browser-repository-archive.js', 'browser-direct-messages.js', 'browser-task-list.js', 'browser-project-planning.js', 'browser-collab.js', 'browser-message-sharing.js', 'browser-work-navigation.js'}:
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const {read}=await import('./src/store.js');console.log(JSON.stringify(read()));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name in {'browser-executions.js','browser-execution-interactions.js'}:
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const {read}=await import('./src/store.js');const s=structuredClone(read());s.executions=[];s.work.find(w=>w.id==='W-104').team={executorIds:['H-lin'],checkerIds:['H-zhou']};console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + " );")
                run(['reload'])
            if path.name == 'browser-execution-dispatch.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const {read}=await import('./src/store.js');const s=structuredClone(read());s.executions=[];s.work.find(w=>w.id==='W-105').team={executorIds:['agent:atlas'],checkerIds:[]};console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + " );")
                run(['reload'])
            if path.name == 'browser-execution-maintenance.js':
                fixture = subprocess.run(['bun', 'tests/execution-maintenance-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-conversation-origin.js':
                fixture = subprocess.run(['bun', 'tests/conversation-origin-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-workflow-list.js':
                fixture = subprocess.run(['bun', 'tests/workflow-list-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-resource-presentation.js':
                fixture = subprocess.run(['bun', 'tests/resource-presentation-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + " );")
                run(['reload'])
            if path.name in {'browser-private-knowledge.js', 'browser-memory-revocation.js'}:
                fixture = subprocess.run(['bun', 'tests/private-knowledge-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-delivery-receipt-history.js':
                fixture = subprocess.run(['bun', 'tests/delivery-receipt-browser-fixture.js'], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-plan-long-content.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');api.update(s=>{s.theme='aurora';s.dark=false;const work=s.work.find(w=>w.id==='W-105');work.id='W-'+('SOURCEIDENTIFIER'.repeat(20));work.title='LongUnbrokenWorkTitle'.repeat(20);work.goalId='G-LONG-CONTENT';s.resources.goals.push({id:work.goalId,name:'LongUnbrokenGoalTitle'.repeat(20),subjectKey:'agent:atlas',ownerKind:'Agent',ownerId:'atlas',status:'进行中',revision:1,definitionRevision:1,krs:[],alignments:[]});});const reportId=api.generatePeriodicReport({subjectKey:'agent:atlas',cadence:'周报',date:'2026-10-14',timezone:'Asia/Singapore'});const s=api.read(),report=s.resources.reports.find(r=>r.id===reportId),work=s.work.find(w=>w.id.startsWith('W-SOURCEIDENTIFIER'));s.testPlanLongContent={reportId,planId:report.nextPlanBinding.planId,workId:work.id,title:work.title,project:work.project,workVersion:work.version,goalId:work.goalId,goalTitle:s.resources.goals.find(g=>g.id===work.goalId).name};console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-operations.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {operationReportBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(await operationReportBrowserFixture(api)));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-plan-links.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {planLinkBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(planLinkBrowserFixture(api)));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-free-task.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {freeTaskBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(freeTaskBrowserFixture(api)));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-regeneration.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {regenerationBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(regenerationBrowserFixture(api)));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], f"localStorage.setItem('collaboration-prototype-v1', {json.dumps(fixture.stdout.strip())})")
                run(['reload'])
            if path.name in {'browser-report-generated-content.js', 'browser-report-generated-adoption.js'}:
                fresh = 'true' if path.name == 'browser-report-generated-content.js' else 'false'
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {generatedContentBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(generatedContentBrowserFixture(api," + fresh + ")));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-current-plan.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {currentPlanBrowserFixture}=await import('./tests/report-lifecycle-fixtures.js');console.log(JSON.stringify(currentPlanBrowserFixture(api)));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-plan-baselines.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');api.reset();const today=new Date().toISOString().slice(0,10);api.update(s=>s.resources.goals.push({id:'G-baseline-fixture',name:'测试场景基线目标',subjectKey:'agent:atlas',ownerId:'atlas',ownerKind:'Agent',status:'进行中',revision:1,definitionRevision:1,krs:[],alignments:[]}));const id=api.openPeriodPlan({subjectKey:'agent:atlas',periodKind:'WEEK',date:today,timezone:'UTC',simulateAgent:true});let p=api.read().resources.plans.find(p=>p.id===id);api.savePeriodPlan(id,{simulateAgent:true,items:[{id:'baseline-item',kind:'FREE_TASK',title:'测试场景未完成研究',goalId:'G-baseline-fixture',freeCategory:'LEARNING',criteria:'提供研究依据',weight:1}]},p.revision);p=api.read().resources.plans.find(p=>p.id===id);api.submitPeriodPlan(id,p.revision,true);p=api.read().resources.plans.find(p=>p.id===id);api.decidePeriodPlan(id,{revision:p.revision,submissionRevision:p.submission.revision,contentHash:p.submission.contentHash,action:'confirm',reason:'测试场景确认范围'});const s=structuredClone(api.read()),base=s.resources.plans.find(p=>p.id===id),b=base.confirmedSnapshot;const variants=[null,{}, {...b,items:{}},{...b,items:[null]},...['revision','subjectKey','periodKind','periodStart','periodEnd','timezone'].map(k=>({...b,[k]:null})),{...b,subjectKey:'human:H-zhou'},{...b,timezone:'Invalid/Zone'},...[0,-1,'2',null].map(weight=>({...b,items:[{...b.items[0],weight}]})),...[{id:{}},{title:{}},{kind:'UNKNOWN'},{dueDate:'2026-02-30'},{kind:'WORK_ITEM',workId:{}}].map(patch=>({...b,items:[{...b.items[0],...patch}]})),{...b,items:[b.items[0],b.items[0]]},{...b,items:[{...b.items[0],weight:Number.MAX_VALUE},{...b.items[0],id:'overflow',weight:Number.MAX_VALUE}]},{...b,revision:0}];s.resources.plans=[{...base,id:'BASELINE-VALID'},{...base,id:'BASELINE-FOREIGN',tenantId:'foreign'},...variants.map((confirmedSnapshot,i)=>({...base,id:'BASELINE-CORRUPT-'+i,confirmedSnapshot}))];for(const field of ['id','kind','title','goalId','krId','workId','freeCategory','criteria','dueDate','note','weight','estimateHours','estimateCostMicros','carriedFromItemId'])s.resources.plans.push({...structuredClone(base),id:'LIVE-CORRUPT-'+field,items:[{...base.items[0],[field]:{malformed:true}}]});for(const [key,items]of [['null',null],['object',{}],['null-item',[null]],['duplicate',[base.items[0],base.items[0]]]])s.resources.plans.push({...structuredClone(base),id:'LIVE-CORRUPT-'+key,items});const nextDate=new Date(base.periodEnd+'T00:00:00Z');nextDate.setUTCDate(nextDate.getUTCDate()+1);const {planPeriod}=await import('./src/plans.js');const nextPeriod=planPeriod('WEEK',nextDate.toISOString().slice(0,10),'UTC');s.resources.plans.push({...base,...nextPeriod,id:'BASELINE-NEXT-CORRUPT',confirmedSnapshot:{...b,...nextPeriod,subjectKey:'human:H-zhou'}});const {upsertPeriodicReport}=await import('./src/reporting.js');upsertPeriodicReport(s,{subjectKey:'agent:atlas',cadence:'周报',date:today,timezone:'UTC'},'显式损坏数据测试',()=> 'BASELINE-REPORT');const {reportSubmissionContent}=await import('./src/report-authorization.js');for(const [kind,date] of [['MISSING','2026-09-07'],['DRAFT','2026-09-14'],['PENDING','2026-09-21'],['FROZEN','2026-09-28']]){const period=planPeriod('WEEK',date,'UTC'),r={id:'CURRENT-'+kind,name:'本期计划测试 '+kind,subjectKey:'human:H-lin',cadence:'周报',...period,timezone:'UTC',references:[],summary:'仅本地核对测试',risk:'',revision:1,status:'草稿'};const p={id:'CURRENT-PLAN-'+kind,tenantId:s.activeTenant,subjectKey:r.subjectKey,periodKind:'WEEK',...period,timezone:'UTC',items:[],revision:2,status:'草稿'};if(kind==='DRAFT')s.resources.plans.push(p);if(kind==='PENDING')s.resources.plans.push({...p,status:'已提交',confirmedSnapshot:{...period,subjectKey:r.subjectKey,periodKind:'WEEK',timezone:'UTC',items:[],revision:1}});if(kind==='FROZEN'){const factSnapshot=api.reportEvidence(s,r);r.status='已提交';r.submission={content:reportSubmissionContent(r),factSnapshot,revision:1,source:'明确本地历史读取测试',time:'2026-10-01T00:00:00Z'};s.resources.plans.push(p);}s.resources.reports.push(r);}console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-audit-list.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}}; const {read}=await import('./src/store.js'); const s=structuredClone(read()); s.events=Array.from({length:501},(_,i)=>({id:'audit-'+i,text:i===0?'LongTitle'+('LongUnbrokenText'.repeat(80)):'核对任务 '+i,actor:'审计员',time:'2026-10-13T08:00:00Z',target:'/p/launch/work-items/W-104?evidence='+i})); console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-content-rendering.js':
                # Untrusted local rendering fixtures only: these reports are
                # drafts with deliberately unavailable evidence, never accepted.
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}}; const {read}=await import('./src/store.js');const {emptyReportContent}=await import('./src/report-content.js');const s=structuredClone(read()),content=emptyReportContent();content.completed=[{key:'long',title:'LongUnbrokenTitle'.repeat(12).slice(0,192),goal_id:'',kr_id:'',plan_item_id:'',fact_ref:{kind:'WORK_ITEM',id:'W-103',event_id:'unverified-rendering-fixture',version:1},note:''}];content.uncompleted=Array.from({length:51},(_,i)=>({key:'u'+i,title:'待核对说明 '+i,plan_item_id:'',work_item_id:'',reason:'OTHER',detail:'作者未核对的渲染样本',carry_over:'ESCALATE',detail_confirmation:null}));content.retrospective={went_well:['布局样本'],problems:['未核对事实'],root_causes:['未提供接受事件'],improvements:[{text:'保留明确阻断状态',propose_memory:false}],ai_drafted:false};const common={owner:'林予',authorId:'H-lin',authorKind:'Human',subjectKey:'human:H-lin',cadence:'日报',timezone:'UTC',periodStart:'2026-10-14',periodEnd:'2026-10-14',references:[],status:'草稿',revision:1,summary:'仅布局与异常数据测试，不认证完成',risk:''};s.resources.reports.unshift({...common,id:'RENDER-LONG',name:'长标题渲染测试',content},{...common,id:'RENDER-MALFORMED',name:'损坏章节渲染测试',periodStart:'2026-10-13',periodEnd:'2026-10-13',content:{}});console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-content-rendering.js':
                run(['eval', '--stdin'], "const s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));const base=s.resources.reports.find(r=>r.id==='RENDER-MALFORMED');const bindings=[{},{snapshot:null},{snapshot:{}},{snapshot:{items:{}}},{snapshot:{items:[null]}},{snapshot:{items:[{}]}},{snapshot:{items:[{id:'item',title:null}]}},{revision:{bad:true},snapshot:{items:[]}},{planId:{bad:true},snapshot:{items:[]}},{status:{bad:true},snapshot:{items:[]}},{snapshot:{periodStart:{bad:true},items:[]}},...['goalId','krId','dueDate','workId','freeCategory'].map(key=>({snapshot:{items:[{id:'item',title:'标题',[key]:{bad:true}}]}}))];bindings.forEach((nextPlanBinding,i)=>s.resources.reports.push({...base,id:'RENDER-PLAN-'+i,name:'损坏固定计划 '+i,content:i%2?null:s.resources.reports.find(r=>r.id==='RENDER-LONG').content,nextPlanBinding}));localStorage.setItem('collaboration-prototype-v1',JSON.stringify(s));")
                run(['reload'])
            if path.name == 'browser-report-authorization.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {prepareStructuredReport,legacyPeriodFixture}=await import('./tests/report-lifecycle-fixtures.js');for(const subjectKey of ['human:H-lin','agent:atlas','tenant:studio','team:ORG-engineering']){const options={subjectKey,cadence:'日报',date:'2026-10-14',timezone:'Asia/Singapore'};if(subjectKey.startsWith('tenant:')||subjectKey.startsWith('team:'))legacyPeriodFixture(api,options);else prepareStructuredReport(api,api.generatePeriodicReport(options));}console.log(JSON.stringify(api.read()));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-report-links.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};const api=await import('./src/store.js');const {prepareStructuredReport}=await import('./tests/report-lifecycle-fixtures.js');for(const [subjectKey,cadence,date] of [['human:H-lin','周报','2000-01-31'],['agent:atlas','周报','2000-01-31'],['human:H-lin','月报','2000-02-01']]){prepareStructuredReport(api,api.generatePeriodicReport({subjectKey,cadence,date,timezone:'Asia/Singapore'}));}console.log(JSON.stringify(api.read()));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            helper = ROOT / 'tests' / 'qa-browser-helpers.js'
            if helper.exists():
                run(['eval', '--stdin'], helper.read_text())
            prerequisites = [{'suite': 'foreground interaction context', 'evidence': foreground}]
            if path.name == 'browser-report-generated-content.js':
                run(['set', 'viewport', str(args.width), '900'])
                run(['eval', '--stdin'], """(async()=>{const s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));location.hash='/reports/'+s.testGeneratedContent.reportId;await new Promise(r=>setTimeout(r,400));[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='编辑').click();await new Promise(r=>setTimeout(r,200));const e=document.querySelector('[aria-label="确认原因与处置 1"]');e.scrollIntoView({block:'center'});e.focus();return true})()""")
                focus_steps = []
                for _ in range(6 if args.width < 700 else 4):
                    run(['press', 'Shift+Tab'])
                    focus_steps.append(decoded(run(['eval', '--stdin'], """(()=>{const e=document.activeElement,r=e.getBoundingClientRect(),h=document.querySelector('dialog[open] .modal-heading').getBoundingClientRect();if(r.top<h.bottom||r.bottom>innerHeight)throw Error('Sticky dialog heading obscures keyboard focus: '+(e.getAttribute('aria-label')||e.textContent));return {pass:true,label:e.getAttribute('aria-label')||e.textContent,top:r.top,headerBottom:h.bottom,bottom:r.bottom}})()""")))
                run(['press', 'Escape'])
                run(['set', 'viewport', str(args.width), '812' if args.width < 700 else '1000'])
                prerequisites.append({'suite': 'native reverse-Tab modal focus', 'steps': focus_steps})
            if path.name == 'browser-defects.js':
                # Regression starts from an actually verified closure. Run its UI
                # prerequisite after this suite's reset, never inject green flags.
                prerequisite = ROOT / 'tests' / 'browser-qa.js'
                outcome = decoded(run(['eval', '--stdin'], prerequisite.read_text()))
                if outcome.get('pass') is not True and outcome.get('result') != 'PASS':
                    raise RuntimeError('Defect QA prerequisite failed: ' + json.dumps(outcome, ensure_ascii=False))
                prerequisites.append({'suite': prerequisite.name, 'evidence': outcome,
                                      'scriptHash': hashlib.sha256(prerequisite.read_bytes()).hexdigest()})
            result = decoded(run(['eval', '--stdin'], path.read_text()))
            if path.name == 'browser-report-free-task.js' and result.get('pass') is True:
                run(['reload'])
                result['fixedHistory'] = decoded(run(['eval', '--stdin'], """(async()=>{await new Promise(r=>setTimeout(r,400));const s=JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=s.testFreeTask,r=s.resources.reports.find(r=>r.id===ids.reportId),text=document.querySelector('main').textContent;if(s.resources.plans.some(p=>p.id===ids.planId))throw Error('Source loss fixture failed');if(!text.includes('明确本地模拟：下游方案比较结论与来源已记录')||!text.includes('本地 Agent 模拟，由 Human H-lin 记录'))throw Error('Fixed FREE evidence/provenance missing after reload');if(document.documentElement.scrollWidth>innerWidth+2)throw Error('FREE evidence reader overflows viewport');if(r.submission.factSnapshot.totals.completed!==0)throw Error('Historical FREE fact became work acceptance');return {pass:true,width:innerWidth,checks:['source Plan absent','fixed evidence and recorder rendered after actual reload','zero invented work acceptance','viewport fit']}})()"""))
            if path.name == 'browser-resource-presentation.js' and result.get('pass') is True:
                original = decoded(run(['eval', '--stdin'], "JSON.parse(localStorage.getItem('collaboration-prototype-v1'))"))
                imported_results = []
                for variant in ['legacy', 'missing', 'unknown', 'malformed', 'requirementsObject', 'requirementsNull', 'requirementsItem']:
                    imported = json.loads(json.dumps(original))
                    imported['testResourcePresentation']['variant'] = variant
                    record = next(item for item in imported['resources']['deliveries'] if item['id'] == imported['testResourcePresentation']['deliveries'][0]['id'])
                    if variant.startswith('requirements'):
                        test = next(item for item in imported['resources']['tests'] if item['id'] == imported['testResourcePresentation']['testId'])
                        test['requirementRefs'] = {'malformed': 'internal-token'} if variant == 'requirementsObject' else None if variant == 'requirementsNull' else ['保留有效条款', {'malformed': 'internal-token'}]
                    elif variant == 'legacy':
                        record.pop('sendFact', None)
                    elif variant == 'missing':
                        record['sendFact'] = {'source': 'LOCAL_SIMULATION'}
                    elif variant == 'unknown':
                        record['sendFact'] = {'source': 'UNVERIFIED_IMPORTED_SOURCE'}
                    else:
                        record['sendFact'] = {'source': 'LOCAL_SIMULATION', 'actor': {'id': {}}, 'deliverySnapshot': {'id': {}, 'name': {}, 'workId': {}, 'recipient': {}, 'version': -1}}
                    run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(json.dumps(imported, ensure_ascii=False)) + ");")
                    run(['reload'])
                    imported_result = decoded(run(['eval', '--stdin'], path.read_text()))
                    if imported_result.get('pass') is not True:
                        raise RuntimeError(json.dumps(imported_result, ensure_ascii=False))
                    imported_results.append(imported_result)
                result['importedViews'] = imported_results
            if path.name == 'browser-report-operations.js' and result.get('pass') is True:
                run(['reload'])
                result['fixedHistory'] = decoded(run(['eval', '--stdin'], """(async()=>{await new Promise(r=>setTimeout(r,400));const s=JSON.parse(localStorage.getItem('collaboration-prototype-v1')),r=s.resources.reports.find(r=>r.id===s.testOperationFactsReportId),regions=[...document.querySelectorAll('main [data-report-operation-evidence]')];if(s.work.some(w=>w.id==='W-103')||s.resources.deliveries.some(d=>d.work==='W-103'))throw Error('Source disappearance fixture failed');if(regions.length!==3||regions.some(e=>!e.textContent.includes('明确本地模拟')||!e.textContent.includes('H-lin')))throw Error('Fixed operation identity/provenance missing after actual reload');if(!document.querySelector('main').textContent.includes('N4'))throw Error('Source loss warning missing');if(r.submission.factSnapshot.totals.completed!==0)throw Error('Operation facts became accepted Work');if(document.documentElement.scrollWidth>innerWidth+2)throw Error('Fixed operation reader overflows viewport');return {pass:true,width:innerWidth,checks:['real reload after source loss','fixed actor/time/local simulation reader','N4 source loss warning','zero Work inflation','viewport fit']}})()"""))
            if path.name == 'browser-routes.js':
                failures = [page for page in result.get('pages', []) if page.get('broken') or page.get('overflow') or not page.get('heading')]
                passed = bool(result.get('pages')) and not result.get('errors') and not failures
            else:
                passed = result.get('pass') is True or result.get('result') == 'PASS'
            if not passed:
                raise RuntimeError(json.dumps(result, ensure_ascii=False))
            if args.capture:
                capture_dir = args.output.parent / (args.output.stem + '-captures')
                capture_dir.mkdir(parents=True, exist_ok=True)
                screenshot = capture_dir / (path.stem + '.png')
                run(['screenshot', str(screenshot.resolve())])
                result['screenshot'] = str(screenshot)
                (capture_dir / (path.stem + '-a11y.json')).write_text(run(['a11y', '--json']) + '\n')
            report['suites'].append({'suite': path.name, 'pass': True, 'evidence': result,
                                    'prerequisites': prerequisites,
                                    'scriptHash': hashlib.sha256(path.read_bytes()).hexdigest()})
            print(path.name, 'PASS', flush=True)
        except BrowserSessionUnsafe:
            raise
        except (RuntimeError, ValueError, subprocess.TimeoutExpired) as error:
            report['suites'].append({'suite': path.name, 'pass': False, 'error': str(error)})
            print(path.name, 'FAIL', str(error)[-900:], flush=True)
except (RuntimeError, subprocess.TimeoutExpired) as error:
    report['setupError'] = str(error)
finally:
    report['sourceUnchanged'] = before == fingerprints()
    report['pass'] = bool(report['suites']) and len(report['suites']) == len(paths) and all(item['pass'] for item in report['suites']) and report['sourceUnchanged'] and 'setupError' not in report
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    try:
        run(['close'])
    except (RuntimeError, subprocess.TimeoutExpired):
        pass
print('Evidence:', args.output, 'PASS' if report['pass'] else 'NOT READY', flush=True)
sys.exit(0 if report['pass'] else 1)
