import { FilePreview, ChatMarkdown } from "./chat-rich";
import { readChatFile, chatFileBlob } from "./chat-content";
import { useViewState } from "./view-state";
import { fileSizeLabel, classifyFilePresentation } from "../../../apps/frontend-web/apps/web/src/lib/file-browser.ts";
import { DataList } from "./data-list";
import React, { useState, useCallback, useRef } from "react";
import { useStore, read, knowledgeLibraryCommand } from "./store";
import { libraryRows, libraryView, libraryWriteProblem, libraryProjectProblem, knowledgeReadProblem } from "./knowledge";
import { PageHead, Section, Button, Icon, Field, Modal, ActionForm, Notice, Badge, Empty, Link, Tabs } from "./ui";
const scopes = { TENANT: "租户库", PROJECT: "项目库", PRIVATE: "我的私人资料", UNBOUND: "待绑定旧资料" };
const states = { PENDING: "待索引", INDEXING: "索引中", INDEXED: "已索引", FAILED: "索引失败", INVALIDATED: "已归档失效" };
const href = view => `/${view.category === "MATERIAL" ? "materials" : "knowledge"}/${view.sourceId}?version=${view.version}${view.publicationId ? `&publication=${view.publicationId}` : ""}${view.referenceId ? `&reference=${view.referenceId}` : ""}`;
function People({ state, value, onChange }) {
  return <fieldset><legend>明确有权读取的 Human</legend>{state.organization.members.filter(person=>person.kind==="Human").map(person=><label className="check-label" key={person.id}><input type="checkbox" checked={value.includes(person.id)} onChange={event=>onChange(event.target.checked?[...value,person.id]:value.filter(id=>id!==person.id))}/>{person.name} · {person.id}</label>)}</fieldset>;
}
function AgentReaders({state,value,onChange}) {
  return <fieldset><legend>明确授权读取此来源的 Agent（默认无授权）</legend>{state.agents.filter(agent=>!["退役","已退役","待批准"].includes(agent.status)).map(agent=><label className="check-label" key={agent.id}><input type="checkbox" checked={value.includes(agent.id)} onChange={event=>onChange(event.target.checked?[...value,agent.id]:value.filter(id=>id!==agent.id))}/>{agent.name} · {agent.id}</label>)}</fieldset>;
}
export function KnowledgeLibrary({ recordId, params = new URLSearchParams(), toast, legacyPage, ResourceEditor, collection = "KNOWLEDGE" }) {
  const s=useStore();
  const category=collection, base=category==="MATERIAL"?"/materials":"/knowledge", label=category==="MATERIAL"?"文件":"知识库";
  const [scopeOverride,setScope]=useViewState(`${category}.scope`,"CURRENT"),[historical,setHistorical]=useViewState(`${category}.historical`,false),[query,setQuery]=useViewState(`${category}.query`,""),[action,setAction]=useState(null),[legacyEdit,setLegacyEdit]=useState(null);
  const [list,setList]=useViewState(`${category}.list`,{query});
  const [, redraw]=useState(0), tenantId=s.activeTenant, workspace=s.project, role=s.role;
  const recheck=useCallback(()=>{const current=read();if(current.activeTenant!==tenantId||current.project!==workspace||current.role!==role)throw Error("资料范围已变化，请重新打开当前列表。");return current;},[tenantId,workspace,role]);
  const refresh=useCallback(()=>{recheck();redraw(value=>value+1);},[recheck]);
  const authorizeExport=useCallback(payload=>{
    const current=recheck();
    return payload.records.every(row=>{const item=current.resources.knowledge.find(item=>item.id===row.source);return item?.library?Boolean(libraryView(current,row.source,{version:row.versionNumber,publicationId:row.publicationId||undefined,referenceId:row.referenceId||undefined})):Boolean(item&&!knowledgeReadProblem(current,item));});
  },[recheck]);
  const scope=category==="MATERIAL"?(s.project==="all"?"TENANT":"PROJECT"):scopeOverride==="CURRENT"?(s.project==="all"?"TENANT":"PROJECT"):scopeOverride;
  const projectId=scope==="PROJECT"?s.project:null, project=s.projects.find(p=>p.id===projectId);
  const record=recordId&&s.resources.knowledge.find(item=>item.id===recordId);
  if (recordId && !record?.library) return legacyPage;
  const view=recordId&&libraryView(s,recordId,{version:params.get("version")||undefined,publicationId:params.get("publication")||undefined,referenceId:params.get("reference")||undefined});
  if (recordId&&!view) return <Empty title="资料不存在或无权访问" text="请返回当前有权访问的资料库；发布版本不开放原件历史。"><Link to={base}>返回当前资料库</Link></Empty>;
  if (!["owner","executor"].includes(s.role)) return <Empty title="当前身份无权访问资料库"/>;
  const lib=record?.library, original=record&&!view.publicationId&&!view.referenceId;
  const problem=record?libraryWriteProblem(s,record):s.offline?"离线中，恢复后再操作。":scope==="PROJECT"?libraryProjectProblem(s,projectId,true):scope==="TENANT"&&s.role!=="owner"?"租户库写入需要负责人权限。":"";
  const begin=(kind,extras={})=>setAction({action:kind,tenantId:s.activeTenant,workspace:s.project,id:record?.id,revision:record?.revision,scope,projectId,category,readers:["H-lin"],agentReaders:[],reason:"",...extras});
  const rows=libraryRows(s,{scope,projectId,category,historical});
  const legacy=s.resources.knowledge.filter(item=>!item.library&&!knowledgeReadProblem(s,item)&&(!item.revocation||historical)&&(["资料","素材","文件"].includes(item.kind)?category==="MATERIAL":category==="KNOWLEDGE")).filter(item=>scope==="UNBOUND"?!["私人","租户"].includes(item.scope):scope==="PRIVATE"?item.scope==="私人":scope==="TENANT"?item.scope==="租户":item.projectId===projectId);
  const listed=[...rows.map(item=>({...item,id:`${item.sourceId}:${item.referenceId||item.publicationId||"root"}`})),...legacy.map(item=>({id:item.id,sourceId:item.id,title:item.name,version:null,state:item.status,category,legacy:item,content:item.content,issues:[]}))];
  const title=view?view.title:category==="MATERIAL"?"文件":scope==="PROJECT"?`${project?.name||"项目不可用"} · ${label}`:`${scopes[scope]} · ${label}`;
  return <div className={category==="MATERIAL"&&!view?"material-page":undefined}>
    <PageHead title={title} subtitle={view?`固定 v${view.version} · ${view.sourceId} · ${view.publicationId?"已授权发布投影":scopes[view.scope]}`:category==="MATERIAL"?"集中管理项目文件与素材，查看固定版本并按权限共享。":"同一原件、明确权限与固定版本；本页上传与索引均为本地模拟。"}>
      {view?<Link className="btn" to={base}>返回资料库</Link>:category!=="MATERIAL"&&scope!=="UNBOUND"&&<><Button disabled={Boolean(problem)} title={problem} onClick={()=>begin("create",{origin:"manual",category})}>{category==="MATERIAL"?"登记文件 / 素材":"新建知识"}</Button><Button disabled={Boolean(problem)} title={problem} onClick={()=>begin("create",{origin:"upload"})}>{category==="MATERIAL"?"上传文件 / 素材":"导入知识文本"}</Button>{scope==="PROJECT"&&<Button disabled={Boolean(problem)} title={problem} onClick={()=>begin("choose-reference")}>引用租户固定版本</Button>}</>}
    </PageHead>
    {s.offline&&<Notice tone="warn">离线中，所有修改均暂停；保留当前阅读。</Notice>}
    {!view&&(category==="MATERIAL"?<MaterialBrowser state={s} rows={listed} scope={scope} scopeOverride={scopeOverride} onScope={setScope} historical={historical} onHistorical={setHistorical} list={list} onList={patch=>setList(previous=>({...previous,...patch}))} onClear={()=>{setHistorical(false);setQuery("");}} refresh={refresh} authorizeExport={authorizeExport} begin={begin} problem={problem} editing={Boolean(action||legacyEdit)} toast={toast}/>:<>
      <Notice>{category==="MATERIAL"?"保存文件、素材原件及固定版本。沟通结论和可复用规则请到治理下的知识库。":"维护可复用知识，可引用沟通、决定、SQL 与应用文档的固定来源。文件原件在协作下的文件与素材中管理。"}</Notice>
      {project?.archivedAt&&<Notice tone="warn">项目已归档，原资料仍在本项目。勾选历史可查阅；恢复项目不会自动重启索引。</Notice>}
      {scope==="UNBOUND"&&<Notice>旧资料未记录稳定 projectId，不根据名称猜测归属。由有权 Human 核对原件后明确绑定。</Notice>}
      <DataList rows={listed} viewId={category} scopeKey={`${tenantId}:${workspace}:${role}:${scope}:${historical}`} title={label} queryState={list} onQueryStateChange={patch=>setList(previous=>({...previous,...patch}))}
        searchText={item=>`${item.title} ${item.sourceId} ${item.content||""}`} onRefresh={refresh} offline={s.offline} editing={Boolean(action||legacyEdit)} onExport={authorizeExport}
        filters={<><Field label="资料范围" value={scopeOverride} options={[{value:"CURRENT",label:s.project==="all"?"当前租户":`当前项目 · ${project?.name||s.project}`},{value:"PRIVATE",label:"我的私人资料"},{value:"UNBOUND",label:"待绑定旧资料"}]} onChange={setScope}/><label className="check-label"><input type="checkbox" checked={historical} onChange={event=>setHistorical(event.target.checked)}/>包含归档 / 撤销历史</label></>}
        onClearFilters={()=>{setScope("CURRENT");setHistorical(false);setQuery("");}}
        columns={[
          {key:"title",label:"标题",value:item=>item.title,exportValue:item=>item.title,render:item=><Link className="resource-card" to={item.legacy?`${base}/${item.sourceId}`:href({...item,category})}>{item.title}</Link>},
          {key:"source",label:"来源编号",value:item=>item.sourceId,exportValue:item=>item.sourceId},
          {key:"versionNumber",label:"固定版本",value:item=>item.version,exportValue:item=>item.version},
          {key:"state",label:"状态",value:item=>item.state,exportValue:item=>item.state,render:item=><Badge>{states[item.state]||item.state}</Badge>},
          {key:"publicationId",label:"发布版",value:item=>item.publicationId||"",exportValue:item=>item.publicationId||""},
          {key:"referenceId",label:"引用",value:item=>item.referenceId||"",exportValue:item=>item.referenceId||""},
        ]}
        renderActions={item=><>{item.legacy?<><Link className="btn" to={`${base}/${item.sourceId}`}>查看详情</Link>{item.legacy.kind!=="记忆"&&!item.legacy.revocation&&<Button disabled={Boolean(libraryWriteProblem(s,item.legacy))} onClick={()=>begin("bind",{id:item.sourceId,revision:item.legacy.revision||1,scope:scope==="UNBOUND"?"PROJECT":scope,projectId:scope==="UNBOUND"?"":projectId,title:item.title})}>绑定范围</Button>}</>:<Link className="btn" to={href({...item,category})}>查看固定版本</Link>}</>}/>

    </>)}
    {view&&<>
      {view.issues.map(issue=><Notice key={issue} tone="warn">{issue}</Notice>)}
      <Section title="固定版本预览" extra={<Badge>{states[view.state]}</Badge>}><p>{view.file?`${view.file.name} · ${view.file.size} bytes · ${view.file.type||"未记录类型"}`:"正文原件"}</p>{view.content===null?<Notice tone="warn">当前授权已撤销，正文不再显示；历史引用标识保留。</Notice>:<pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{view.content}</pre>}<p>{view.authorization.basis||`${view.authorization.applicability} · 许可：${view.authorization.license} · 脱敏：${view.authorization.redaction} · 来源 v${view.authorization.sourceVersion}：${view.authorization.sourceNote}`}</p>
      {view.publicationId&&<Notice>这里只显示 Human 确认的固定发布版本；原件历史、旧正文和私人元数据不会开放。</Notice>}
      </Section>
      {original&&<>
        <Section title="原件版本与索引"><p>Agent 当前明确授权：{(lib.agentReaders||[]).join("、")||"无"}</p><Button disabled={Boolean(problem)} onClick={()=>begin("grant-agents",{agentReaders:lib.agentReaders||[]})}>管理原件 Agent 授权</Button><Field label="查看原件版本" value={String(view.version)} onChange={version=>{location.hash=`${base}/${record.id}?version=${version}`}} options={lib.versions.filter(v=>v.purpose==="ORIGINAL").map(v=>({value:String(v.number),label:`v${v.number} · ${v.title}`}))}/><p>索引状态：{states[lib.indexState]} · 已索引版本 {lib.indexedVersion??"无"} · 尝试 {lib.attempts}</p>{lib.indexReason&&<p>{lib.indexReason}</p>}<Notice>索引过程和结果由下面的明确模拟动作演示，不代表真实文件服务或检索服务已完成。</Notice>
          <div className="actions">{["PENDING","INDEXED","FAILED"].includes(lib.indexState)&&<Button disabled={Boolean(problem)} onClick={()=>begin("index")}>{lib.indexState==="FAILED"?"重试模拟索引":"开始 / 重建模拟索引"}</Button>}{lib.indexState==="INDEXING"&&<><Button disabled={Boolean(problem)} onClick={()=>begin("index-success")}>模拟索引成功</Button><Button disabled={Boolean(problem)} onClick={()=>begin("index-failure")}>模拟索引失败</Button></>}{["upload","manual"].includes(lib.origin)&&<Button disabled={Boolean(problem)} onClick={()=>begin(lib.origin==="upload"?"replace":"revise",{title:view.title,content:view.content,origin:lib.origin})}>{lib.origin==="upload"?"替换内容为新版本":"修订知识为新版本"}</Button>}{lib.origin==="upload"&&<Button disabled={Boolean(problem)} onClick={()=>begin("archive")}>归档上传来源</Button>}{lib.scope!=="TENANT"&&<Button disabled={Boolean(problem)} onClick={()=>begin("propose",{title:view.title,content:"",readers:["H-lin"]})}>提出租户共享候选</Button>}</div>{problem&&<Notice tone="warn">{problem}</Notice>}
        </Section>
        <Section title="共享候选与固定发布版">{lib.publications.length?lib.publications.map(pub=><article className="section" key={pub.id}><h3>{lib.versions.find(v=>v.number===pub.version)?.title} · v{pub.version}</h3><p>{pub.status} · 原件 v{pub.sourceVersion} · {pub.applicability}</p><p>脱敏：{pub.redaction}；许可：{pub.license}；来源：{pub.sourceNote}</p><p>可读 Human：{pub.readers.join("、")}</p>{pub.status==="PENDING"&&<pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{lib.versions.find(v=>v.number===pub.version)?.content}</pre>}<div className="actions">{pub.status==="PENDING"&&["approve","reject"].map(kind=><Button key={kind} disabled={Boolean(problem)||s.role!=="owner"} onClick={()=>begin(kind,{publicationId:pub.id})}>{kind==="approve"?"确认固定发布版":"拒绝候选"}</Button>)}{["APPROVED","REVOKED"].includes(pub.status)&&<Link className="btn" to={href({category,sourceId:record.id,version:pub.version,publicationId:pub.id})}>查看授权发布投影</Link>}{pub.status==="APPROVED"&&<><Button disabled={Boolean(problem)} onClick={()=>begin("grant-agents",{publicationId:pub.id,agentReaders:pub.agentReaders||[]})}>管理发布版 Agent 授权</Button><Button disabled={Boolean(problem)||s.role!=="owner"} onClick={()=>begin("revoke-publication",{publicationId:pub.id})}>撤销发布授权</Button></>}</div>{pub.decisionReason&&<p>{pub.decisionReason}</p>}</article>):<Empty title="尚无共享候选"/>}</Section>
        <Section title="固定引用与变更影响">{lib.references.length?lib.references.map(ref=><p key={ref.id}>项目 {s.projects.find(p=>p.id===ref.projectId)?.name||ref.projectId} · v{ref.version} · {ref.revokedAt?"已撤销":lib.archivedAt?"来源失效":!ref.publicationId&&ref.version!==lib.currentVersion?"有更新，基线未移动":"固定引用"} <Link to={href({category,sourceId:record.id,version:ref.version,referenceId:ref.id})}>查看固定引用</Link></p>):<p>尚无项目引用。</p>}</Section>
        <Section title="操作历史">{lib.events.map((event,index)=><p key={index}>{event.time} · {event.id} · {event.action} · {event.reason} · 本地模拟</p>)}</Section>
      </>}
      <Section title="固定来源取用核对"><Notice>只核对此版本、用途与 Agent 当前明确授权；本地模拟，未调用 runtime，不声称真实检索或缓存失效。</Notice><Button disabled={s.offline||view.content===null} onClick={()=>begin("check-use",{version:view.version,publicationId:view.publicationId,referenceId:view.referenceId,agentId:"",purpose:"",allowHistorical:false})}>模拟核对 Agent 取用</Button>{view.useChecks.map(check=><p key={check.id}><Badge>{check.outcome==="AUTHORIZED"?"核对允许 · 未执行":"核对拒绝"}</Badge> {check.agentId} · v{check.version} · {check.purpose} · {check.reason} · {check.actorId} · {check.time} · LOCAL_SIMULATION</p>)}</Section>
      {view.referenceId&&<Button disabled={Boolean(libraryProjectProblem(s,view.referencedProjectId,true))||s.offline} onClick={()=>begin("revoke-reference",{referenceId:view.referenceId})}>撤销此项目引用</Button>}
    </>}
    {legacyEdit&&legacyEdit.tenantId===s.activeTenant&&legacyEdit.workspace===s.project&&<ResourceEditor kind="knowledge" record={legacyEdit.record} onClose={()=>setLegacyEdit(null)} onSaved={()=>{setLegacyEdit(null);toast("原记录已保存");}}/>}
    {action&&action.tenantId===s.activeTenant&&action.workspace===s.project&&<LibraryAction key={`${action.action}:${action.id||"new"}`} initial={action} state={s} onClose={()=>setAction(null)} toast={toast}/>}
  </div>;
}
// Presentation follows the existing FilesPage / FileBrowser; domain writes still
// use the shared material-library commands and permissions, never the real IAM API.
function MaterialBrowser({state:s,rows,scope,onScope,historical,onHistorical,list,onList,onClear,refresh,authorizeExport,begin,problem,editing,toast}) {
  const [layout,setLayout]=useViewState("materials.layout","details"),[previewId,setPreview]=useViewState("materials.preview",""),[type,setType]=useViewState("materials.type","");
  const selected=rows.find(row=>row.id===previewId);
  const project=s.projects.find(project=>project.id===s.project);

  const fileName=row=>row.file?.name||row.title;
  const presentation=row=>classifyFilePresentation({name:fileName(row),detectedType:row.file?.type});
  const typeLabels={document:"文档",spreadsheet:"表格",presentation:"演示",pdf:"PDF",image:"图片",video:"视频",audio:"音频",text:"文本",code:"代码",archive:"压缩包",unknown:"其他"};
  const fileType=row=>typeLabels[presentation(row).category]||"其他";
  const icon=row=>({image:"Image",video:"Clapperboard",code:"FileCode2",audio:"Volume2"})[presentation(row).category]||"FileText";
  const readCurrent=row=>{const current=read();if(current.activeTenant!==s.activeTenant||current.project!==s.project||current.role!==s.role)throw Error("文件范围已变化，请重新打开。");const record=current.resources.knowledge.find(record=>record.id===row.sourceId);if(!record)throw Error("文件不存在。");if(record.library){const view=libraryView(current,row.sourceId,{version:row.version,publicationId:row.publicationId||undefined,referenceId:row.referenceId||undefined});if(!view||view.content===null)throw Error("文件不存在或当前无权读取。");return view;}if(knowledgeReadProblem(current,record))throw Error("当前无权读取文件。");return {...row,title:record.name,content:record.content};};
  const preview=selected?(()=>{try{return readCurrent(selected);}catch{return null;}})():null;
  const download=row=>{try{const view=readCurrent(row);if(view.file&&!view.file.data&&view.file.textContentAvailable!==true)throw Error("此文件仅登记了元数据，原始文件未上传，不能下载二进制内容。");const blob=view.file?.data?chatFileBlob({...view.file,kind:"file"}):new Blob([view.content||""],{type:"text/plain;charset=utf-8"}),url=URL.createObjectURL(blob),anchor=document.createElement("a");anchor.href=url;anchor.download=view.file?.name||(/\.[a-z0-9]+$/i.test(view.title)?view.title:`${view.title}.txt`);document.body.appendChild(anchor);anchor.click();anchor.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);toast("已下载当前有权读取的固定文本版本。");}catch(error){toast(error.message,true);}};
  const columns=[
    {key:"name",label:"名称",value:row=>fileName(row),exportValue:row=>fileName(row),render:row=><button className="file-name" onClick={()=>setPreview(row.id)}><Icon name={icon(row)} size={24}/><span><strong>{fileName(row)}</strong><small>{row.version?`固定版本 v${row.version}`:"待核对归属的旧文件"}</small></span></button>},
    {key:"type",label:"类型",value:fileType,exportValue:fileType},
    {key:"size",label:"大小",value:row=>row.file?.size,exportValue:row=>row.file?.size??null,render:row=>fileSizeLabel(row.file?.size,"zh-CN")},
    {key:"state",label:"状态",value:row=>row.state,exportValue:row=>row.state,render:row=><Badge>{states[row.state]||row.state}</Badge>},
    {key:"source",visible:false,label:"来源编号",value:row=>row.sourceId,exportValue:row=>row.sourceId},
    {key:"versionNumber",visible:false,label:"固定版本",value:row=>row.version,exportValue:row=>row.version},
    {key:"publicationId",visible:false,label:"发布版",value:row=>row.publicationId||"",exportValue:row=>row.publicationId||""},
    {key:"referenceId",visible:false,label:"引用",value:row=>row.referenceId||"",exportValue:row=>row.referenceId||""},
  ];
  const locationLabel=scope==="PROJECT"?project?.name:scope==="TENANT"?"租户文件":scope==="PRIVATE"?"我的私人文件":"待绑定旧文件";
  return <div className="material-file-browser">
    <div className="material-file-main"><div className="material-file-location"><Icon name="Folders"/><strong>{locationLabel}</strong><Button icon="Upload" disabled={Boolean(problem)||scope==="UNBOUND"} title={problem} onClick={()=>begin("create",{origin:"upload"})}>上传文件</Button><Button disabled={Boolean(problem)||scope==="UNBOUND"} title={problem} onClick={()=>begin("create",{origin:"manual"})}>新建文本</Button>{scope==="PROJECT"&&<Button disabled={Boolean(problem)} title={problem} onClick={()=>begin("choose-reference")}>引用租户固定版本</Button>}<div className="file-view-toggle" aria-label="文件显示方式"><Button aria-pressed={layout==="details"} onClick={()=>setLayout("details")}>列表</Button><Button aria-pressed={layout==="large"} onClick={()=>setLayout("large")}>图标</Button></div></div>
      <DataList compact searchPlaceholder="搜索文件名称或内容…" emptyText={<div className="file-empty"><Icon name="Folders" size={36}/><strong>{rows.length?"没有符合条件的文件":"此位置暂无文件"}</strong><span>{rows.length?"调整搜索词或清空筛选条件。":"上传文件或新建文本，集中保存协作材料。"}</span></div>} rows={rows} columns={columns} viewId="materials" scopeKey={`${s.activeTenant}:${s.project}:${s.role}:${scope}:${historical}`} title="文件与素材" queryState={list} onQueryStateChange={onList} defaultSort="name" onRefresh={refresh} onExport={authorizeExport} offline={s.offline} editing={editing}
        searchText={row=>`${fileName(row)} ${row.sourceId} ${row.content||""}`} filterRow={row=>!type||fileType(row)===type}
        filters={<><Field label="文件类型" value={type} onChange={value=>{setType(value);onList({page:1});}} options={[{value:"",label:"全部类型"},...Array.from(new Set(rows.map(fileType))).map(value=>({value,label:value}))]}/><label className="check-label"><input type="checkbox" checked={historical} onChange={event=>onHistorical(event.target.checked)}/>包含归档历史</label>{layout!=="details"&&<Field label="排序" value={list.sort??"name"} onChange={sort=>onList({sort,page:1})} options={[{value:"",label:"默认顺序"},{value:"name",label:"名称"},{value:"size",label:"大小"},{value:"type",label:"类型"}]}/>}</>}
        onClearFilters={()=>{setType("");onClear();}}
        renderCards={layout==="large"?row=><button className="file-card" key={row.id} onClick={()=>setPreview(row.id)}><Icon name={icon(row)} size={40}/><strong>{fileName(row)}</strong><small>{fileType(row)} · {fileSizeLabel(row.file?.size,"zh-CN")}</small></button>:undefined}
        renderActions={row=><><Button onClick={()=>setPreview(row.id)}>预览</Button><Link className="btn" to={row.legacy?`/materials/${row.sourceId}`:href({...row,category:"MATERIAL"})} target="_blank" rel="noopener noreferrer">详情</Link></>}/>
    </div>
    {previewId&&<Modal wide className="chat-material-dialog material-preview-dialog" title={preview?.title||"文件不可访问"} onClose={()=>setPreview("")} headerActions={preview&&<Link className="btn" to={selected.legacy?`/materials/${selected.sourceId}`:href({...selected,category:"MATERIAL"})} target="_blank" rel="noopener noreferrer">新标签页打开详情</Link>}>
      {preview?<><div className="chat-material-toolbar"><span>{fileName(selected)} · {fileType(selected)} · {fileSizeLabel(selected.file?.size,"zh-CN")} · {selected.version?`固定 v${selected.version}`:"历史文件"}</span><Button disabled={Boolean(preview.file&&!preview.file.data&&preview.file.textContentAvailable!==true)} onClick={()=>download(selected)}>{preview.file?.data?"下载原文件":"下载当前文本版本"}</Button></div><div className="chat-material-body">{preview.file?.data?<FilePreview file={{...preview.file,id:selected.id,name:fileName(selected)}}/>:preview.file&&preview.file.textContentAvailable!==true?<Empty title="原文件尚未连接" text="当前记录仅保留历史元数据，未补造内容。"/>:/\.(md|markdown)$/i.test(fileName(selected))?<ChatMarkdown text={preview.content||""}/>:<pre className="file-preview-text">{preview.content||"空文本文件"}</pre>}{preview.issues?.map(issue=><Notice tone="warn" key={issue}>{issue}</Notice>)}</div></>:<Empty title="文件不存在或无权访问" text="未展示缓存中的旧正文；可关闭后重新选择有权文件。"/>}

    </Modal>}
  </div>;
}
function LibraryAction({initial,state,onClose,toast}) {
  const [form,setForm]=useState(initial),[fileError,setFileError]=useState("");
  const uploadGeneration=useRef(0);
  const change=(key,value)=>setForm(old=>({...old,[key]:value}));
  const creating=["create","bind"].includes(form.action),contentAction=["create","replace","revise","propose"].includes(form.action),promotion=form.action==="propose";
  const [choices]=useState(()=>libraryRows(state,{scope:"TENANT",category:initial.category,historical:false}).map(view=>({...view,sourceRevision:state.resources.knowledge.find(item=>item.id===view.sourceId).revision})));
  const [selection,setSelection]=useState("");
  const actionTitles={create:form.category==="MATERIAL"?(form.origin==="upload"?"上传文件":"新建文本文件"):"新建知识",bind:"确认资料归属",revise:"保存知识新版本",replace:"替换文件内容",archive:"归档文件来源","choose-reference":"引用租户固定版本",propose:"提出固定共享候选",approve:"Human 确认发布",reject:"拒绝共享候选","grant-agents":"管理 Agent 读取授权","check-use":"核对固定来源取用",index:"开始模拟索引","index-success":"记录模拟索引成功","index-failure":"记录模拟索引失败","revoke-publication":"撤销发布授权","revoke-reference":"撤销固定引用"};
  const title=actionTitles[form.action]||"核对资料变更",submitLabel=form.action==="create"?(form.category==="MATERIAL"?"保存文件":"保存知识"):actionTitles[form.action]||"保存变更";
  return <Modal title={title} wide draftValue={form} onClose={onClose}><ActionForm label={submitLabel} onCancel={onClose} onSubmit={({dismiss})=>{
    if(fileError)throw Error(fileError);
    let input=form;
    if(form.action==="choose-reference") {const chosen=choices.find(item=>`${item.sourceId}:${item.publicationId||""}`===selection);if(!chosen)throw Error("请选择有权读取的租户版本。");input={...form,action:"reference",id:chosen.sourceId,revision:chosen.sourceRevision,publicationId:chosen.publicationId,version:chosen.version};}
    knowledgeLibraryCommand(input);dismiss();toast("资料库本地记录已更新");
  }}>
    {creating&&<><Field label="明确归属范围" value={form.scope} onChange={value=>change("scope",value)} disabled={form.category==="MATERIAL"} options={[{value:"TENANT",label:"租户"},{value:"PROJECT",label:"项目"},...(form.category==="MATERIAL"?[]:[{value:"PRIVATE",label:"私人"}])]}/>{form.scope==="PROJECT"&&<Field disabled={form.category==="MATERIAL"} label="所属项目（稳定身份）" value={form.projectId||""} onChange={value=>change("projectId",value)} options={[{value:"",label:"请选择项目"},...state.projects.filter(p=>p.id!=="private").map(p=>({value:p.id,label:`${p.name} · ${p.id}${p.archivedAt?" · 已归档":""}`}))]}/>}<Field disabled label="资料类别" value={form.category} onChange={value=>change("category",value)} options={[{value:"KNOWLEDGE",label:"知识"},{value:"MATERIAL",label:"素材与资料"}]}/><People state={state} value={form.readers} onChange={value=>change("readers",value)}/></>}
    {contentAction&&<><Field label={promotion?"发布版本标题":"标题"} value={form.title||""} onChange={value=>change("title",value)} required/><Field label={promotion?"明确允许公开给所选 Human 的脱敏正文":"正文 / 本地预览内容"} type="textarea" value={form.content||""} onChange={value=>change("content",value)} required/>{form.origin==="upload"&&<><label className="field">选择本地文件（文本≤10 MB；其他≤1 MB）<input type="file" onChange={async event=>{const file=event.target.files?.[0];if(!file)return;const generation=++uploadGeneration.current;setFileError("正在读取文件，请稍候。");if(file.size>10*1024*1024){setFileError("文件超过 10 MB，未读取。");return;}try{const textual=classifyFilePresentation({name:file.name,detectedType:file.type}).preview==="text";const original=!textual?await readChatFile(file):null;const text=textual?await file.text():`文件原件：${file.name}`;if(generation!==uploadGeneration.current)return;setForm(old=>({...old,title:old.title||file.name,content:text,file:{name:file.name,size:file.size,type:file.type,textContentAvailable:textual,...(original?{data:original.data}: {})}}));setFileError("");}catch(error){if(generation===uploadGeneration.current)setFileError(error.message||"本地文件读取失败，请重新选择。");}}}/></label><Notice>所选文件保存在本地原型中，可预览和下载；未上传到服务器。</Notice>{fileError&&<p role="alert">{fileError}</p>}</>}</>}
    {creating&&<AgentReaders state={state} value={form.agentReaders||[]} onChange={value=>change("agentReaders",value)}/>}
    {promotion&&<><AgentReaders state={state} value={form.agentReaders||[]} onChange={value=>change("agentReaders",value)}/><Notice>候选不会改变原件范围。Human 仅批准以下固定脱敏版本；原件历史不随发布开放。</Notice><People state={state} value={form.readers} onChange={value=>change("readers",value)}/>{[["applicability","适用范围"],["redaction","脱敏核对说明"],["license","许可依据"],["sourceNote","来源与固定版本依据"]].map(([key,label])=><Field key={key} label={label} value={form[key]||""} onChange={value=>change(key,value)} required/>)}</>}
    {form.action==="choose-reference"&&<><Field label="有权采用的租户版本" value={selection} onChange={setSelection} options={[{value:"",label:"请选择固定版本"},...choices.map(item=>({value:`${item.sourceId}:${item.publicationId||""}`,label:`${item.title} · v${item.version} · ${item.sourceId}`}))]}/><Notice>只保存原件 ID、固定版本和授权依据；更新不会自动移动项目基线。</Notice></>}
    {form.action==="grant-agents"&&<><Notice>授权仅作用于所选原件或固定发布版，不继承频道权限。清空后新取用核对将拒绝，旧核对历史保留。</Notice><AgentReaders state={state} value={form.agentReaders||[]} onChange={value=>change("agentReaders",value)}/></>}
    {form.action==="check-use"&&<><Field label="Agent 稳定身份" value={form.agentId||""} onChange={value=>change("agentId",value)} options={[{value:"",label:"请选择 Agent"},...state.agents.map(agent=>({value:agent.id,label:`${agent.name} · ${agent.id}`}))]}/><p>固定来源 {form.id} · v{form.version}</p><Field label="明确的本次取用目的" value={form.purpose||""} onChange={value=>change("purpose",value)} required/><label className="check-label"><input type="checkbox" checked={form.allowHistorical===true} onChange={event=>change("allowHistorical",event.target.checked)}/>明确允许为此目的核对历史版本 / 已归档项目来源</label></>}
    {form.action==="archive"&&<Notice tone="warn">仅归档此 upload 来源并立即失效，不物理删除原件。默认检索与新引用排除它；已有执行和项目的固定引用仍能按原权限回看。</Notice>}
    {form.action.startsWith("index")&&<Notice>此操作明确模拟索引状态；失败保留旧版本，重试不伪造真实服务完成。</Notice>}
    {form.action==="approve"&&<Notice>Human 确认候选中的范围、脱敏、许可与来源，决定绑定当前原件及固定发布版本。</Notice>}
    <Field label="操作理由 / 失败原因 / 历史使用目的" type="textarea" value={form.reason||""} onChange={value=>change("reason",value)} required/>
  </ActionForm></Modal>;
}
