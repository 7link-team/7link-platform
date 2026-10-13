/* Same-origin bridge; the independent React Flow runtime never blocks the vanilla prototype. */
function wfTemplateState(domain) {
  try {
    const saved = JSON.parse(localStorage.getItem('prototype-workflow-' + domain));
    if (saved?.definition?.schema_version === 1 && Array.isArray(saved.history) && saved.history.length) return saved;
  } catch {}
  const definition = WorkflowDefinition.seed(domain);
  return { definition, history: [definition], patches: [] };
}
function wfPublishedDefinition(domain, version) {
  const history = wfTemplateState(domain).history;
  return version ? history.find(def => def.version === version) || null : history.at(-1) || WorkflowDefinition.seed(domain);
}
function wfVersionRows(domain) {
  return wfTemplateState(domain).history.map((def, index) => [def.version, '已发布', def.published_at || '一方初始样例', index ? 'Morgan · 人工审核后模拟发布；' + def.nodes.length + ' 节点 / ' + def.edges.length + ' 连线' : '一方初始发布版本']);
}
function wfVersionDiff(domain) {
  const versions = wfTemplateState(domain).history;
  return versions.length < 2 ? '只有初始发布版本；草稿不会计入发布历史。' : versions.slice(1).map((def, index) => {
    const old = versions[index], added = def.nodes.filter(n => !old.nodes.some(o => o.id === n.id)), removed = old.nodes.filter(n => !def.nodes.some(o => o.id === n.id)), changed = def.nodes.filter(n => old.nodes.some(o => o.id === n.id && JSON.stringify({label:o.label,config:o.config}) !== JSON.stringify({label:n.label,config:n.config})));
    return `<p>${tpEsc(old.version)} → ${tpEsc(def.version)}：新增 ${added.length}，移除 ${removed.length}，修改 ${changed.length} 节点；连线 ${old.edges.length} → ${def.edges.length}。${added.map(n => tpEsc(n.label)).join('、')}</p>`;
  }).join('');
}
function wfLoopSummary(domain) {
  return wfPublishedDefinition(domain).nodes.filter(n => n.kind === 'LOOP').map(n => `${n.config.maxIterations} 轮 / 预算 ${n.config.maxCost} / 升级给 ${n.config.escalationOwner || '未配置'}`).join('；') || '没有循环节点';
}
function wfStageProjection(domain) {
  const def = wfPublishedDefinition(domain);
  return '<p class="muted small">当前发布版本 ' + tpEsc(def.version) + '；以下阶段与依赖仅投影 WorkflowDef，不是另一份编辑定义。</p>' + tpTable(['节点 / 类型','依赖来源','节点配置'], def.nodes.map(n => [tpEsc(n.label) + '<br>' + tpBadge(WorkflowDefinition.kinds[n.kind], n.kind === 'HUMAN_GATE' ? 'warn' : 'info'), def.edges.filter(e => e.target === n.id).map(e => tpEsc(def.nodes.find(parent => parent.id === e.source)?.label || e.source) + (e.label ? ' · ' + tpEsc(e.label) : '')).join('<br>') || '流程入口', '<code class="small" style="overflow-wrap:anywhere">' + tpEsc(JSON.stringify(n.config)) + '</code>']));
}
Object.entries(tpTemplates).forEach(([domain, metadata]) => {
  Object.defineProperties(metadata, {
    version: { get: () => wfPublishedDefinition(domain).version },
    stages: { get: () => wfPublishedDefinition(domain).nodes.map(n => `${n.label}|${WorkflowDefinition.kinds[n.kind]}${n.kind === 'HUMAN_GATE' ? '|必经人工关口' : ''}`) }
  });
});
function wfFrame(domain, project, readonly = false) {
  return `<iframe title="${readonly ? "项目只读流程实例" : "可视化流程编辑器"}" src="prototype/workflow-editor.html?domain=${encodeURIComponent(domain)}&project=${encodeURIComponent(project || "p1")}${readonly ? "&readonly=1" : ""}" style="width:100%;height:${readonly ? 610 : 1050}px;border:1px solid var(--border);border-radius:12px;background:white" loading="lazy"></iframe>`;
}
const wfTemplatePage = PAGES.template;
PAGES.template = (domain) => {
  if (tpState.tabs[domain] === 9) {
    const t = tpTemplates[domain];
    if (!t) return tpMissing("模板");
    return (
      head(
        "领域模板",
        t.name + " · 流程",
        "唯一 WorkflowDef JSON · 真正 React Flow 编辑器 · 开发 v1.1 / 原型模拟",
        tpBtn("返回模板库", "go('templates')"),
      ) +
      tpTabs(domain, [
        "概览",
        "阶段",
        "产物",
        "验证器",
        "评分标准",
        "内部质量门",
        "评测集",
        "生成器",
        "版本历史",
        "流程",
      ]) +
      wfFrame(domain, CURPROJ || "p1")
    );
  }
  return wfTemplatePage(domain).replace(
    '<div class="tabs"',
    `<div class="row" style="margin:12px 0">${tpBtn("编辑流程 · v1.1", `tpTab('${domain}',9)`, "primary")}</div><div class="tabs"`,
  );
};
const wfPipelinePage = PAGES.pipelines;
PAGES.pipelines = (project) =>
  wfPipelinePage(project) +
  card(
    "项目流程实例 · 只读",
    wfFrame(
      { p5: "VIDEO", p6: "NOVEL", p7: "TEAM_TASKS" }[project] || "SOFTWARE",
      project || CURPROJ || "p1",
      true,
    ),
  );
window.addEventListener("message", (event) => {
  if (
    event.origin !== location.origin ||
    ![...document.querySelectorAll("iframe")].some(
      (frame) =>
        frame.contentWindow === event.source &&
        frame.src.includes("/workflow-editor.html"),
    )
  )
    return;
  const data = event.data;
  if (data?.type === "workflow-open-work" && typeof data.id === "string") {
    if (pjWork.some((work) => work.id === data.id)) go("workitem/" + data.id);
    return;
  }
  if (data?.type !== "workflow-instance-request") return;
  const project = data.project,
    current =
      location.hash.replace(/^#\/?/, "").split("/")[1] || CURPROJ || "p1";
  if (project !== current) return;
  const domain = {p5:'VIDEO',p6:'NOVEL',p7:'TEAM_TASKS'}[project] || 'SOFTWARE';
  const lockedVersion = pjProject(project)?.templateVersion?.match(/\d+\.\d+\.\d+/)?.[0] || '1.0.0';
  const definition = wfPublishedDefinition(domain, lockedVersion);
  if (!definition) {
    event.source.postMessage({type:'workflow-instance',project,projectName:projectName(project),definition:{version:lockedVersion,nodes:[],edges:[]},items:[],node_runs:[],error:'锁定的已发布定义不可用；不会替换为其他版本'},location.origin);
    return;
  }
  const allItems = WI.filter(item=>item.p===project), items=allItems.slice(0,40);
  const node_runs = items.flatMap(item=>{
    const facts=lsRunFacts[item.id], stages=facts?.stages || [];
    const quality=!stages.length ? 'NOT_RUN' : stages.slice(0,4).includes('失败') ? '失败' : stages.slice(0,4).includes('无法判定') ? '无法判定' : stages.slice(0,4).length===4 && stages.slice(0,4).every(s=>['通过','跳过'].includes(s)) ? '通过' : '等待';
    const executionEntry=Object.entries(exExecutions).find(([,e])=>e.wi===item.id), execution=executionEntry?.[1];
    return definition.nodes.map(node=>{
      const bound={n4:'WORK_ITEM',n5:'LOOP',n9:'HUMAN_GATE',n10:'HUMAN_GATE'}[node.id]===node.kind;
      return {node_id:node.id,work_item_id:item.id,version:definition.version,execution:bound ? facts?.execution || executionEntry?.[0] || null : null,round:bound ? facts?.round || 0 : 0,
      status: !bound ? 'NOT_RUN' : node.id==='n4' ? execution?.state || 'NOT_RUN' : node.id==='n5' ? quality : node.id==='n9' ? item.s==='ACCEPTED'&&item.l>=3 ? '通过' : stages[4] || 'NOT_RUN' : node.id==='n10' ? item.l===4 ? '通过' : stages[5] || 'NOT_RUN' : 'NOT_RUN',
      source:bound ? '工作项 / 执行 / 平台质量门样例事实' : '未绑定节点运行事实'};
    });
  });
  event.source.postMessage({type:'workflow-instance',project,projectName:projectName(project),definition,items:items.map(item=>({id:item.id})),node_runs,total:allItems.length},location.origin);
});
