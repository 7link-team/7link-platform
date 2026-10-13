/* Shared prototype actions. Local state only; no credentials or network services are changed. */
const shellState={clientNotices:[true,true,true],gateway:{enabled:false,machine:'m1',model:'gpt-6-astra',key:'DEMO-'+crypto.randomUUID()}};
function shellAppearance(){return card('外观',`<div class="row wrap"><button type="button" class="btn" onclick="toggleDark()">切换明暗</button><label class="stack small">主题<select class="select" onchange="setTheme(this.value)">${['aurora','modern','business','eyeCare','pinkKawaii','neoBrutalism'].map(t=>`<option ${document.documentElement.dataset.theme===t?'selected':''}>${t}</option>`).join('')}</select></label></div>`);}
function shellExportAudit(){
  if(VIEWER!=='team')return;
  const rows=[...document.querySelectorAll('#view table tbody tr')].map(row=>[...row.cells].map(cell=>cell.textContent.trim()));
  const data={prototype:true,signature:null,note:'浏览器样例导出，不是服务端签名审计证据',rows,localEvents:cpState.logs};
  const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));
  const a=document.createElement('a');a.href=url;a.download='audit-sample.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);toast('已导出样例 JSON；不含真实签名');
}
const shellAudit=PAGES.audit;
PAGES.audit=()=>shellAudit().replace("toast('导出（底座导出与签名）')",'shellExportAudit()');
const shellPolicy=PAGES.policy;
PAGES.policy=()=>shellPolicy().replace("toast('升级规则编辑器')","cpState.policyTab='设置';route()");
PAGES.gateway=()=>{
  const state=shellState.gateway,base=location.protocol==='file:'?'http://localhost:8080/v1':location.origin+'/v1';
  return head('实验性 · 浏览器演示','API 网关','配置和密钥仅保存在本次浏览器内存；此原型不启动真实 API 网关。','<span class="badge b-warn">实验</span>')+`<div class="grid g12"><div class="c6">${card('网关配置',`<div class="stack"><label class="row"><input id="shell-gw-enabled" type="checkbox" ${state.enabled?'checked':''}>启用网关（模拟）</label><label class="stack small">路由机器<select id="shell-gw-machine" class="select">${MACHINES.filter(m=>m.st==='ONLINE').map(m=>`<option value="${m.id}" ${state.machine===m.id?'selected':''}>${qaEsc(m.n)}</option>`).join('')}</select></label><label class="stack small">默认模型<input id="shell-gw-model" class="input" maxlength="100" value="${qaEsc(state.model)}"></label><div id="shell-gw-error" role="alert"></div><button type="button" class="btn primary" onclick="shellGatewaySave()">保存配置</button><p class="muted small">仅文本、单密钥、最多 8 个并发、180 秒超时；运行契约未在原型执行。</p></div>`)}</div><div class="c6">${card('接入信息',`<div class="stack"><label class="stack small">示例 Base URL<input class="input mono" readonly value="${qaEsc(base)}"></label><button type="button" class="btn" onclick="shellCopy('${base}')">复制示例地址</button><label class="stack small">本地演示密钥<input class="input mono" readonly value="${qaEsc(state.key.slice(0,9))}••••${qaEsc(state.key.slice(-4))}"></label><div class="row wrap"><button type="button" class="btn" onclick="shellGatewayKey('view')">查看演示密钥</button><button type="button" class="btn danger" onclick="shellGatewayKey('rotate')">轮换演示密钥</button></div><p class="muted small">DEMO 密钥没有服务端权限。正式产品查看与轮换都需要绑定对象的重新认证和审计。</p></div>`)}</div></div>`;
};
function shellGatewaySave(){
  const state=shellState.gateway,model=document.getElementById('shell-gw-model').value.trim(),machine=document.getElementById('shell-gw-machine').value;
  if(!model||!MACHINES.some(m=>m.id===machine&&m.st==='ONLINE')){document.getElementById('shell-gw-error').textContent='填写模型并选择在线机器。';return;}
  Object.assign(state,{enabled:document.getElementById('shell-gw-enabled').checked,model,machine});route();toast('配置已保存在本次演示；没有启动服务');
}
async function shellCopy(value){try{await navigator.clipboard.writeText(value);toast('已复制')}catch{openModal(`<div class="modal-h"><h3>复制内容</h3></div><div class="modal-b"><label class="stack">浏览器未允许自动复制，请选中后复制<input class="input mono" readonly value="${qaEsc(value)}" onclick="this.select()"></label></div><div class="modal-f"><button class="btn" onclick="closeModal()">关闭</button></div>`);}}
function shellGatewayKey(action){openModal(`<div class="modal-h"><h3>${action==='rotate'?'轮换':'查看'}演示密钥</h3></div><div class="modal-b stack"><p>只影响浏览器样例，不操作真实凭证或请求。</p><label class="stack small">理由<textarea id="shell-gw-reason" maxlength="2000"></textarea></label><label class="row"><input id="shell-gw-auth" type="checkbox">已完成模拟重新认证（不是实际身份认证）</label><div id="shell-gw-key-error" role="alert"></div></div><div class="modal-f"><button class="btn" onclick="closeModal()">取消</button><button class="btn primary" onclick="shellGatewayKeySave('${action}')">确认模拟操作</button></div>`);}
function shellGatewayKeySave(action){
  const reason=document.getElementById('shell-gw-reason').value.trim();if(!reason||!document.getElementById('shell-gw-auth').checked){document.getElementById('shell-gw-key-error').textContent='填写理由并完成模拟认证。';return;}
  if(action==='rotate')shellState.gateway.key='DEMO-'+crypto.randomUUID();
  cpState.logs.push({action:'gateway.key_'+action,object:'prototype-gateway',actor:'morgan',reason,reauthentication:'SIMULATED_ONLY'});closeModal();
  if(action==='view')openModal(`<div class="modal-h"><h3>本地演示密钥</h3></div><div class="modal-b"><label class="stack">DEMO 密钥<input class="input mono" readonly value="${qaEsc(shellState.gateway.key)}" onclick="this.select()"></label></div><div class="modal-f"><button class="btn" onclick="closeModal()">关闭</button></div>`);else{route();toast('演示密钥已轮换；追加本地审计记录');}
}
/* Connect native labels to the existing sibling controls without replacing their handlers. */
function shellLabelControls(){
  for(const root of [document.getElementById('view'),document.getElementById('modal'),document.getElementById('drawer')]){
    if(!root)continue;let index=0;
    for(const label of root.querySelectorAll('label.f')){
      const input=label.nextElementSibling;if(!input||!input.matches('input,select,textarea'))continue;
      if(!input.id)input.id='prototype-'+root.id+'-field-'+index++;label.htmlFor=input.id;
    }
  }
}
const shellIcons=icons;
icons=function(){shellLabelControls();shellIcons();};
