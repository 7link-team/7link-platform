/* Shared WorkflowDef seeds: the canvas and template projections use one definition. */
(()=>{
const kinds = {
  WORK_ITEM: "工作项",
  HUMAN_GATE: "人工关口",
  CONDITION: "条件分支",
  FAN_OUT: "数据驱动扇出",
  LOOP: "内部质量循环",
  SUBFLOW: "子流程",
  TRANSFORM: "Transform",
};
const defaults = {
  WORK_ITEM: { owner: "执行者", acceptance: ["验收标准-01"] },
  HUMAN_GATE: { audience: "team", required: true, owner: "交付负责人" },
  CONDITION: { jsonLogic: { "==": [{ var: "quality.passed" }, true] } },
  FAN_OUT: {
    source: "requirement_confirmation.items",
    itemKey: "id",
    maxItems: 20,
    freezeAfter: "SIGN_OFF",
    expansion_key: "confirmation_item_id",
  },
  LOOP: {
    maxIterations: 5,
    maxCost: 50,
    onExhausted: "ESCALATE_TO_HUMAN",
    escalationOwner: "交付负责人",
    until: { "==": [{ var: "quality.passed" }, true] },
  },
  SUBFLOW: { workflowRef: "internal-quality@1.0.0" },
  TRANSFORM: {
    mapping: { input: "artifacts.source", output: "artifacts.normalized" },
  },
};
const clone = (x) => JSON.parse(JSON.stringify(x));
function seed(domain) {
  const nodes = [
    ["WORK_ITEM", "需求澄清", 0, 40],
    ["HUMAN_GATE", "需求确认 · 甲方", 230, 40],
    ["FAN_OUT", "按确认单条目拆解", 460, 40],
    ["WORK_ITEM", "实现与产物", 690, 40],
    ["LOOP", "内部质量门 · 最多5轮", 920, 40],
    ["CONDITION", "质量通过？", 1150, 40],
    ["TRANSFORM", "整理交付证据", 1380, 0],
    ["SUBFLOW", "交付子流程", 1610, 0],
    ["HUMAN_GATE", "团队人工接受", 1840, 0],
    ["HUMAN_GATE", "甲方交付签收", 2070, 0],
  ].map(([type, label, x, y], i) => ({
    id: "n" + (i + 1),
    kind: type,
    display: { position: { x, y } },
    label,
    input_bindings: {},
    output_bindings: {},
    config: {
      ...clone(defaults[type]),
      ...(i === 1 || i === 9 ? { audience: "client" } : {}),
    },
  }));
  const labels = {
    VIDEO: [
      "Brief 与脚本",
      "甲方脚本确认",
      "按镜头表生成候选",
      "镜头生成与选用",
      "视频 QC · 最多5轮",
      "QC 通过？",
      "母版与来源清单",
      "视频交付子流程",
      "版权与创意人工接受",
      "甲方成片签收",
    ],
    NOVEL: [
      "故事前提与大纲",
      "甲方大纲确认",
      "按章节计划起草",
      "章节与故事状态",
      "连续性编辑 · 最多5轮",
      "连续性通过？",
      "整理全书稿",
      "小说交付子流程",
      "主题版权人工接受",
      "甲方书稿签收",
    ],
    TEAM_TASKS: [
      "需求收集与拆解",
      "人工需求确认",
      "按任务清单分派",
      "人类执行与交付",
      "同组互审 · 最多5轮",
      "清单与证据齐全？",
      "状态报告整理",
      "团队交付子流程",
      "指定角色人工接受",
      "甲方交付签收",
    ],
  }[domain];
  if (labels)
    nodes.forEach((n, i) => {
      n.label = labels[i];
      if (n.kind === "FAN_OUT")
        n.config.source =
          domain === "VIDEO"
            ? "shot_list.items"
            : domain === "NOVEL"
              ? "chapter_plan.items"
              : "requirement_confirmation.items";
      if (n.kind === "SUBFLOW")
        n.config.workflowRef = domain.toLowerCase() + "-delivery@1.0.0";
    });
  const edges = nodes
    .slice(1)
    .map((n, i) => ({
      id: "e" + i,
      source_port: "out",
      target_port: "in",
      source: nodes[i].id,
      target: n.id,
      ...(i === 5 ? { label: "通过", branch: "true" } : {}),
    }));
  nodes.push({
    id: "n11",
    kind: "WORK_ITEM",
    input_bindings: {},
    output_bindings: {},
    label: "内部返工",
    display: { position: { x: 1380, y: 190 } },
    config: {...clone(defaults.WORK_ITEM), outcome: 'REWORK'},
  });
  edges.push({
    id: "e10",
    source_port: "false",
    target_port: "in",
    source: "n6",
    target: "n11",
    label: "不通过",
    branch: "false",
  });
  return {
    schema: "WorkflowDef",
    schema_version: 1,
    workflow_id: domain.toLowerCase() + "-workflow",
    name: domain + " 流程",
    parameters: {},
    entry_node_id: "n1",
    output_bindings: {},
    version: "1.0.0",
    status: "published",
    domain,
    nodes,
    edges,
  };
}

function jsonLogic(value) {
  if (value === null || ['string','number','boolean'].includes(typeof value)) return true;
  if (Array.isArray(value)) return value.every(jsonLogic);
  if (!value || typeof value !== 'object' || Object.keys(value).length !== 1) return false;
  const [op] = Object.keys(value), allowed = new Set(['var','missing','missing_some','if','==','===','!=','!==','!','!!','and','or','>','>=','<','<=','+','-','*','/','%','min','max','in','cat','substr','merge','map','filter','reduce','all','some','none']);
  return allowed.has(op) && jsonLogic(value[op]);
}
function validate(def, domain) {
  if (!def || typeof def !== 'object') return ['WorkflowDef须为对象'];
  const errors=[], object=v=>v && typeof v==='object' && !Array.isArray(v), nodes=def.nodes, edges=def.edges;
  if (!Array.isArray(nodes)||!Array.isArray(edges)) return ['节点与连线须为数组'];
  if (nodes.some(n=>!object(n)) || edges.some(e=>!object(e))) return ['节点与连线须为对象'];
  const ids=new Set(nodes.map(n=>n.id)), byId=new Map(nodes.map(n=>[n.id,n]));
  if(def.schema!=='WorkflowDef'||def.schema_version!==1||!def.workflow_id||!def.name||!object(def.parameters)||!object(def.output_bindings))errors.push('缺少WorkflowDef必填字段或schema版本无效');
  if(!nodes.length)errors.push('流程不能为空');
  if(nodes.length>200)errors.push('原型单图最多200节点，请拆为子流程');
  if(ids.size!==nodes.length)errors.push('节点ID重复');
  if(!ids.has(def.entry_node_id))errors.push('流程入口必须引用现有节点');
  for(const n of nodes){
    if(!n.id||!n.label?.trim()||!kinds[n.kind]||!object(n.config)||!object(n.input_bindings)||!object(n.output_bindings)){errors.push((n.label||n.id)+'：节点类型、名称、配置或bindings无效');continue;}
    const c=n.config;
    if(!object(n.display?.position)||!Number.isFinite(n.display.position.x)||!Number.isFinite(n.display.position.y))errors.push(n.label+'：节点须有有限数值布局坐标');
    if(n.kind==='WORK_ITEM'&&(!c.owner||!Array.isArray(c.acceptance)||!c.acceptance.length))errors.push(n.label+'：工作项须有执行者与验收标准');
    if(n.kind==='HUMAN_GATE'&&(c.required!==true||!['team','client'].includes(c.audience)||!String(c.owner||'').trim()))errors.push(n.label+'：必经人工关口须有TEAM/CLIENT范围与人类负责人');
    if(n.kind==='LOOP'){
      if(!Number.isInteger(c.maxIterations)||c.maxIterations<3||c.maxIterations>8)errors.push(n.label+'：内部轮次应为3–8的整数');
      if(typeof c.maxCost!=='number'||!Number.isFinite(c.maxCost)||c.maxCost<=0)errors.push(n.label+'：循环须有有限正数预算maxCost');
      if(!object(c.until)||!jsonLogic(c.until))errors.push(n.label+'：循环须有有效JSON Logic停止条件until');
      if(c.onExhausted!=='ESCALATE_TO_HUMAN'||typeof c.escalationOwner!=='string'||!c.escalationOwner.trim())errors.push(n.label+'：循环须有停止后的人工升级策略和主体');
    }
    if(n.kind==='CONDITION'){
      if(!object(c.jsonLogic)||!jsonLogic(c.jsonLogic))errors.push(n.label+'：须有有效JSON Logic，无任意代码/I/O');
      const branches=edges.filter(e=>e.source===n.id).map(e=>e.branch||e.source_port);
      if(branches.length!==2||!branches.includes('true')||!branches.includes('false'))errors.push(n.label+'：须有唯一true与false分支');
    }
    if(n.kind==='FAN_OUT'&&(!c.source||!c.itemKey||!Number.isInteger(c.maxItems)||c.maxItems<1||!c.expansion_key||c.freezeAfter!=='SIGN_OFF'))errors.push(n.label+'：扇出须有数据来源、条目键、有限上限及签认后冻结expansion_key');
    if(n.kind==='SUBFLOW'&&(typeof c.workflowRef!=='string'||!/@\d+\.\d+\.\d+$/.test(c.workflowRef)))errors.push(n.label+'：子流程须绑定明确语义版本');
    if(n.kind==='SUBFLOW'&&c.workflowRef?.split('@')[0]===def.workflow_id)errors.push(n.label+'：子流程不可递归引用自身');
    if(n.kind==='TRANSFORM'&&(!object(c.mapping)||!c.mapping.input||!c.mapping.output))errors.push(n.label+'：声明式映射须有输入和输出');
  }
  const seedDef=seed(domain), required=seedDef.nodes.filter(n=>n.kind==='HUMAN_GATE');
  for(const gate of required){const found=byId.get(gate.id);if(!found||found.kind!=='HUMAN_GATE'||found.config?.required!==true||found.config?.audience!==gate.config.audience)errors.push(gate.label+'：模板必经关口不可移除或降级');}
  const incoming=new Map(nodes.map(n=>[n.id,[]])),outgoing=new Map(nodes.map(n=>[n.id,[]])),edgeIds=new Set();
  for(const e of edges){
    if(!e.id||edgeIds.has(e.id)||!e.source_port||!e.target_port)errors.push('连线ID重复或端口缺失');edgeIds.add(e.id);
    if(!ids.has(e.source)||!ids.has(e.target)){errors.push('连线引用了不存在的节点');continue;}
    incoming.get(e.target).push(e.source);outgoing.get(e.source).push(e.target);
  }
  const degrees=new Map([...incoming].map(([id,list])=>[id,list.length])),queue=[...degrees].filter(([,count])=>count===0).map(([id])=>id),order=[];
  while(queue.length){const id=queue.shift();order.push(id);for(const target of outgoing.get(id)){degrees.set(target,degrees.get(target)-1);if(degrees.get(target)===0)queue.push(target);}}
  if(order.length!==nodes.length)errors.push('外层图必须为静态DAG；重复执行仅由LOOP的有界语义定义');
  const reachable=new Set(),pending=[def.entry_node_id];while(pending.length){const id=pending.pop();if(!ids.has(id)||reachable.has(id))continue;reachable.add(id);pending.push(...outgoing.get(id));}
  if(reachable.size!==nodes.length)errors.push('所有节点必须从唯一入口可达；存在游离或未连接节点');
  if(incoming.get(def.entry_node_id)?.length)errors.push('流程入口不可有前置依赖');
  if(order.length===nodes.length&&reachable.size===nodes.length){
    const dominators=new Map();for(const id of order){if(id===def.entry_node_id){dominators.set(id,new Set([id]));continue;}const parents=incoming.get(id),first=dominators.get(parents[0])||new Set();const common=new Set([...first].filter(candidate=>parents.every(parent=>dominators.get(parent)?.has(candidate))));common.add(id);dominators.set(id,common);}
    const completionExits=nodes.filter(n=>!outgoing.get(n.id).length&&n.config?.outcome!=='REWORK');
    if(!completionExits.length)errors.push('流程须有经过交付关口的完成出口');
    for(const gate of nodes.filter(n=>n.kind==='HUMAN_GATE'&&n.config?.required===true)){
      const targets=new Set(completionExits.map(n=>n.id)),walk=seedDef.edges.filter(e=>e.source===gate.id).map(e=>e.target);
      while(walk.length){const id=walk.pop();if(targets.has(id))continue;targets.add(id);walk.push(...seedDef.edges.filter(e=>e.source===id).map(e=>e.target));}
      for(const target of targets)if(ids.has(target)&&!dominators.get(target)?.has(gate.id))errors.push(gate.label+'：入口至'+byId.get(target).label+'的路径绕过必经关口');
    }
  }
  return [...new Set(errors)];
}
globalThis.WorkflowDefinition={kinds,defaults,clone,seed,validate};
})();
