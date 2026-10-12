/* 项目预览汇总：仅浏览器内存样例，查看器复用远程工作区。 */
const pvState={project:'',status:'正常',type:'全部',woken:{}};
const pvServices=[
  {id:'web',name:'网页 · 账单导出',exec:'EX-9080',work:'BIL-142',machine:'m1',kind:'工作站',port:5173,source:'项目宪法启动',status:'运行中',label:'p-7f3k9q2m',item:'web'},
  {id:'api',name:'接口 · 导出接口',exec:'EX-9080',work:'BIL-142',machine:'m1',kind:'工作站',port:8080,source:'Agent 上报且已校验属于本执行进程',status:'已休眠 30 分钟',label:'p-4m8q2k7s',item:'api'},
  {id:'board',name:'看板 · 字段排序进度',exec:'EX-9102',work:'BIL-143',machine:'m3',kind:'服务器',port:3000,source:'项目宪法启动',status:'启动中',label:'p-6n2r8t4w',item:'web'},
  {id:'check',name:'接口 · 字段校验',exec:'EX-9102',work:'BIL-143',machine:'m3',kind:'服务器',port:8081,source:'Agent 上报且已校验属于本执行进程',status:'失败',label:'p-9h5c3v8b',item:'api',failure:'健康检查失败：启动进程退出，端口 8081 未监听'}
];
const pvArtifacts=[
  {id:'a1',work:'BIL-142',type:'网页',icon:'monitor',name:'账单导出网页',version:'9f3c2a1',time:'2026-10-12 10:12',exec:'EX-9080',item:'web',comments:3,delivery:false},
  {id:'a2',work:'BIL-142',type:'接口',icon:'braces',name:'导出接口定义',version:'9f3c2a1',time:'2026-10-12 10:10',exec:'EX-9080',item:'api',comments:2,delivery:false},
  {id:'a3',work:'BIL-142',type:'文档',icon:'file-text',name:'导出验证报告 PDF',version:'产物第 3 版',time:'2026-10-12 10:08',exec:'EX-9080',item:'pdf',comments:1,delivery:false},
  {id:'a4',work:'BIL-142',type:'源码',icon:'file-code',name:'导出实现 src/export.ts',version:'9f3c2a1',time:'2026-10-12 10:05',exec:'EX-9080',item:'web',file:'src/export.ts',comments:4,delivery:false},
  {id:'a5',work:'BIL-142',type:'文件',icon:'file-spreadsheet',name:'账单样例 exports/billing.csv',version:'产物第 2 版',time:'2026-10-12 10:07',exec:'EX-9080',item:'csv',comments:2,delivery:false},
  {id:'a6',work:'BIL-142',type:'视频',icon:'film',name:'账单导出操作演示',version:'产物第 1 版',time:'2026-10-12 10:14',exec:'EX-9080',item:'video',comments:1,delivery:false},
  {id:'a7',work:'BIL-142',type:'小说',icon:'book-open',name:'潮水来信 · 示例章节',version:'产物第 2 版',time:'2026-10-12 10:15',exec:'EX-9080',item:'novel',comments:2,delivery:false},
  {id:'a8',work:'BIL-143',type:'文件',icon:'image',name:'字段排序候选截图',version:'产物第 1 版',time:'2026-10-12 10:18',exec:'EX-9102',item:'image',comments:0,delivery:false},
  {id:'a9',work:'BIL-135',type:'文档',icon:'file-text',name:'失败重试交付说明',version:'产物第 3 版',time:'2026-10-11 16:20',exec:'EX-9080',item:'pdf',comments:0,delivery:true}
];
function pvEsc(pvValue){return String(pvValue==null?'':pvValue).replace(/[&<>"']/g,pvChar=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[pvChar]));}
function pvBtn(pvText,pvAction,pvClass='',pvDisabled=false,pvReason=''){return `<span class="stack" style="gap:4px;align-items:flex-start"><button type="button" class="btn ${pvClass}" onclick="${pvEsc(pvAction)}" ${pvDisabled?'disabled':''}>${pvText}</button>${pvDisabled?`<span class="muted xs">${pvEsc(pvReason)}</span>`:''}</span>`;}
function pvSet(pvKey,pvValue){pvState[pvKey]=pvValue;route();}
function pvChips(pvKey,pvValues){return `<div class="row wrap">${pvValues.map(pvValue=>`<button type="button" class="chip ${pvState[pvKey]===pvValue?'on':''}" aria-pressed="${pvState[pvKey]===pvValue}" onclick="pvSet('${pvKey}','${pvValue}')">${pvValue}</button>`).join('')}</div>`;}
function pvTable(pvHeaders,pvRows,pvEmpty){return `<div class="table-wrap"><table><thead><tr>${pvHeaders.map(pvHeader=>`<th scope="col">${pvHeader}</th>`).join('')}</tr></thead><tbody>${pvRows.join('')||`<tr><td colspan="${pvHeaders.length}"><div class="empty">${pvEmpty}</div></td></tr>`}</tbody></table></div>`;}
function pvStatus(pvService){return pvState.woken[pvService.id]?'启动中':pvState.status==='无运行中服务'?(pvService.status==='运行中'?'已休眠 30 分钟':pvService.status):pvService.status;}
function pvOpen(pvExec,pvItem,pvTab='预览',pvStatusValue='正常',pvFile='src/export.ts'){
  if(typeof rmState==='undefined')return toast('远程工作区脚本尚未加载');
  rmState.tab=pvTab;rmState.item=pvItem;rmState.status=pvStatusValue;rmState.version=pvTab==='文件'?'声明提交 9f3c2a1（隔离副本重启）':'当前工作区';rmState.file=pvFile;rmState.mode='只读观看';
  go('workspace/'+pvExec);
}
function pvOpenService(pvId){const pvService=pvServices.find(pvRow=>pvRow.id===pvId);if(!pvService)return;const pvCurrent=pvStatus(pvService);if(pvCurrent!=='运行中')return toast('服务尚未运行，请等待启动或先唤醒');pvOpen(pvService.exec,pvService.item);}
function pvWake(pvId){const pvService=pvServices.find(pvRow=>pvRow.id===pvId);if(!pvService||pvStatus(pvService)!=='已休眠 30 分钟')return;pvState.woken[pvId]=true;route();toast('模拟唤醒已请求，等待健康检查；未启动真实服务');}
function pvView(pvId){const pvArtifact=pvArtifacts.find(pvRow=>pvRow.id===pvId);if(pvArtifact)pvOpen(pvArtifact.exec,pvArtifact.item,pvArtifact.type==='源码'?'文件':'预览','正常',pvArtifact.file);}
async function pvCopy(pvId){const pvService=pvServices.find(pvRow=>pvRow.id===pvId);if(!pvService)return;const pvURL=new URL(location.href);pvURL.hash='/workspace/'+pvService.exec;try{await navigator.clipboard.writeText(pvURL.href);toast('内部工作区链接已复制，访问仍需项目权限');}catch(pvError){openModal(`<div class="stack" style="padding:24px"><h3>复制内部链接</h3><p class="small">你可以复制下面的内部工作区链接；进入后选择${pvEsc(pvService.name)}。</p><input aria-label="内部工作区链接" readonly value="${pvEsc(pvURL.href)}" style="width:100%">${pvBtn('关闭','closeModal()','ghost')}</div>`);}}
PAGES['previews']=(pvArg)=>{
  const pvProjectId=pvArg||'p1',pvProject=PROJECTS.find(pvRow=>pvRow.id===pvProjectId);
  if(pvState.project!==pvProjectId){pvState.project=pvProjectId;pvState.status='正常';pvState.type='全部';pvState.woken={};}
  const pvEmpty=pvState.status==='空项目'||pvProjectId!=='p1';
  const pvServiceRows=pvEmpty?[]:pvServices.map(pvService=>{
    const pvCurrent=pvStatus(pvService),pvMachine=MACHINES.find(pvRow=>pvRow.id===pvService.machine),pvReason=pvCurrent==='启动中'?'健康检查中，端口尚未就绪':pvCurrent==='失败'?'启动失败，需在执行中排查日志':'服务已休眠，请先唤醒';
    return `<tr><td><b>${pvEsc(pvService.name)}</b></td><td><span class="mono">${pvService.exec} / ${pvService.work}</span><div class="muted small">${pvEsc((WI.find(pvRow=>pvRow.id===pvService.work)||{}).t)}</div></td><td>${pvEsc(pvMachine?pvMachine.n:'执行机不可用')}<br><span class="badge b-info">${pvService.kind}</span></td><td><span class="mono">${pvService.port}</span><div class="small">${pvService.source}</div></td><td><span class="badge ${pvCurrent==='运行中'?'b-ok':pvCurrent==='失败'?'b-danger':'b-warn'}">${pvCurrent}</span>${pvService.failure?`<div class="muted xs">${pvService.failure}</div>`:''}</td><td><span class="mono small">${pvEsc(pvService.label+'.<预览域名>')}</span>${pvBtn('复制内部链接',`pvCopy('${pvService.id}')`,'ghost sm')}</td><td><div class="row wrap">${pvBtn('打开预览',`pvOpenService('${pvService.id}')`,'primary sm',pvCurrent!=='运行中',pvReason)}${pvCurrent==='已休眠 30 分钟'?pvBtn('唤醒',`pvWake('${pvService.id}')`,'sm'):''}</div></td></tr>`;
  });
  const pvVisible=pvEmpty?[]:pvArtifacts.filter(pvArtifact=>pvState.type==='全部'||pvArtifact.type===pvState.type);
  const pvGroups=[...new Set(pvVisible.map(pvArtifact=>pvArtifact.work))];
  const pvProducts=pvGroups.map(pvWork=>`<div class="stack"><div class="row wrap"><b>${pvWork} · ${pvEsc((WI.find(pvRow=>pvRow.id===pvWork)||{}).t)}</b>${S((WI.find(pvRow=>pvRow.id===pvWork)||{}).s)}</div>${pvTable(['产物 / 类型','版本 / 生成时间','来源执行','面向甲方','批注','操作'],pvVisible.filter(pvArtifact=>pvArtifact.work===pvWork).map(pvArtifact=>`<tr><td>${I(pvArtifact.icon)} <b>${pvEsc(pvArtifact.name)}</b><div class="small">${pvArtifact.type} ${['视频','小说'].includes(pvArtifact.type)?'<span class="badge b-stale">v1.1</span>':''}</div></td><td><span class="mono">${pvArtifact.version}</span><div class="muted xs">${pvArtifact.time}</div></td><td class="mono">${pvArtifact.exec}</td><td><span class="badge ${pvArtifact.delivery?'b-ok':'b-stale'}">${pvArtifact.delivery?'已进交付包 BIL#3':'未交付'}</span></td><td>${pvArtifact.comments} 条</td><td>${pvBtn('查看',`pvView('${pvArtifact.id}')`,'sm')}</td></tr>`),'此任务暂无产物')}</div>`).join('<div class="sep"></div>');
  return head('项目空间 › 交付', '预览与产物', `${pvProject?pvEsc(pvProject.name):'项目不可用'} · 这里汇总本项目所有可预览的东西；预览用于查看进度，不作为验收证据`)+
    card('状态预览',`<div class="stack">${pvChips('status',['正常','无运行中服务','空项目'])}<div class="note info">浏览器内样例；服务状态与唤醒为模拟。预览域名来自部署配置，显示为遮罩。你仍需登录并拥有项目执行读取权限。</div></div>`)+
    card('运行中的本地服务（实时预览）',pvTable(['服务','所属执行与任务','执行机与类型','端口与来源','状态','预览地址','操作'],pvServiceRows,'本项目暂无运行中的服务；等待项目宪法启动或 Agent 上报并通过归属校验。')+'<p class="muted small">空闲 30 分钟自动休眠；执行结束即停止。预览通过执行机出站隧道访问，不开放入站端口。</p>')+
    card('交付物与产物',`<div class="stack">${pvChips('type',['全部','网页','接口','视频','文档','源码','文件','小说'])}${pvProducts||`<div class="empty">${pvEmpty?'本项目暂无交付物与产物。':'此类型暂无产物，请切换到全部。'}</div>`}<div class="note info">批注绑定产物版本；查看、锚定批注与富媒体播放统一进入远程工作区。进入交付包仅代表面向甲方，不代表甲方已验收。</div></div>`)+
    card('源码',pvEmpty?'<div class="empty">本项目暂无声明提交；执行者提交声明后在这里显示仓库、分支与提交版本。</div>':pvTable(['仓库 / 分支','最近声明提交','所属任务 / 执行','操作'],[
      `<tr><td><b>账单中心应用仓库</b><div class="mono small">feature/billing-export</div></td><td><span class="mono">9f3c2a1</span><div class="small">2026-10-12 10:05 · 已声明完成（未验证）</div></td><td class="mono">BIL-142 / EX-9080</td><td>${pvBtn('浏览源码',"pvOpen('EX-9080','web','文件')",'primary sm')}</td></tr>`,
      `<tr><td><b>账单中心应用仓库</b><div class="mono small">feature/billing-field-order</div></td><td>尚无声明提交<div class="muted xs">最近工作区提交 6d7e4b2 · 不是声明版本</div></td><td class="mono">BIL-143 / EX-9102</td><td>${pvBtn('浏览源码','', 'sm',true,'执行仍在进行中，尚未声明完成')}</td></tr>`
    ],'暂无声明提交')+'<div class="note info">源码以提交为准，评审看的是声明完成的那一版。浏览源码进入只读文件标签，可查看与基线的差异。</div>')+
    (!pvEmpty?'<div class="note warn">查看器接线说明：现有远程工作区仅提供 EX-9080 样例；EX-9102 入口保留真实执行编号，目标页会提示样例尚未提供。源码入口已选择声明版本，但现有文件查看器仍展示当前工作区差异；精确提交快照需由查看器补齐。</div>':'');
};
TITLES['previews']='预览与产物';
NAVPARENT['previews']='project';
