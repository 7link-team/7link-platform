import React, { useEffect, useLayoutEffect, useRef, useState, useId } from "react";
import { Button, Field, Notice, Select } from "./ui";
import { useViewState } from "./view-state";
import { queryList, projectListExport, listRowId, defaultRowId } from "./list-query";

// rows must already be authorized by the business page on every render. The list
// does not read the store, own a second record cache, grant permissions or import facts.
// onRefresh re-reads local business data; onExport rechecks permission for the
// projected IDs/fields and may throw/return false. Its return value is never exported.
export function DataList({ rows, columns, viewId, scopeKey, title = "资源列表", getRowId = defaultRowId,
  searchText, filterRow, filters, onClearFilters, renderActions, defaultSort = "", defaultOrder = "asc",
  defaultPageSize = 20, pageSizes = [10,20,50,100], onCreate, createLabel = "新增", createDisabledReason = "",
  onImport, importLabel = "导入", importDisabledReason = "", onExport, exportDisabledReason = "", exportSource,
  queryState, onQueryStateChange, renderCards, compact = false, searchPlaceholder, onRefresh, offline = false, editing = false, background = false, error, emptyText = "当前条件下没有记录。" }) {
  const availableColumns=columns.filter(column=>column.visible!==false);
  const statePrefix = `${viewId || "list"}:${scopeKey || "scope"}`;
  const [columnPreferences,setColumnPreferences]=useViewState(`${statePrefix}:columns`,{});
  const hiddenColumns=columnPreferences && !Array.isArray(columnPreferences) ? columnPreferences : {};
  const selectedColumns=availableColumns.filter(column=>hiddenColumns[column.key]!==false);
  const visibleColumns=selectedColumns.length?selectedColumns:availableColumns.slice(0,1);
  const listRoot=useRef(null), viewportRoot=useRef(null);
  useLayoutEffect(()=>{
    const root=listRoot.current;if(!root)return;
    const measure=()=>root.style.setProperty("--list-available-height",`${Math.max(420,window.innerHeight-Math.max(0,root.getBoundingClientRect().top)-24)}px`);
    measure();window.addEventListener("resize",measure);
    return()=>window.removeEventListener("resize",measure);
  });
  // Controlled hosts merge each patch into their own URL-backed state (including modal hosts).
  const bind = (name, local, fallback) => queryState == null ? local : [queryState[name] ?? fallback, value => onQueryStateChange?.({[name]:value})];
  const [query,setQuery]=bind("query",useViewState(`${statePrefix}:query`,""),""), [sort,setSort]=bind("sort",useViewState(`${statePrefix}:sort`,defaultSort),defaultSort),
    [order,setOrder]=bind("order",useViewState(`${statePrefix}:order`,defaultOrder),defaultOrder), [page,setPage]=bind("page",useViewState(`${statePrefix}:page`,1),1),
    [pageSize,setPageSize]=bind("pageSize",useViewState(`${statePrefix}:pageSize`,defaultPageSize),defaultPageSize), [interval,setIntervalValue]=bind("refresh",useViewState(`${statePrefix}:refresh`,"0"),"0"),
    [scroll,setScroll]=bind("scroll",useViewState(`${statePrefix}:scroll`,{top:0,left:0}),{top:0,left:0});
  const [hidden,setHidden]=useState(()=>typeof document !== "undefined" && document.hidden), [refreshing,setRefreshing]=useState(false),
    [lastSuccess,setLastSuccess]=useState(null), [refreshError,setRefreshError]=useState(""), [operationError,setOperationError]=useState(""),
    [exporting,setExporting]=useState(false), [receipt,setReceipt]=useState(""), [countdown,setCountdown]=useState(0);
  const scroller=useRef(null), alive=useRef(true), refreshingRef=useRef(false), refreshToken=useRef(0), exportingRef=useRef(false), latest=useRef(null), regionId=useId();
  const paused = offline ? "离线" : editing ? "正在编辑，保留草稿" : background || hidden ? "后台标签" : "";
  latest.current={rows,columns,getRowId,searchText,filterRow,query,sort,order,page,pageSize,pageSizes,scopeKey,onRefresh,onExport,exportDisabledReason,paused,error,source:exportSource || viewId};
  let result, queryError="";
  try { result=queryList(rows,{columns,getRowId,searchText,filterRow,query,sort,order,page,pageSize,pageSizes}); }
  catch(cause){queryError=cause.message || "列表查询失败。";}
  useEffect(()=>{alive.current=true;return()=>{alive.current=false;refreshToken.current++;};},[]);
  useEffect(()=>{const changed=()=>setHidden(document.hidden);document.addEventListener("visibilitychange",changed);return()=>document.removeEventListener("visibilitychange",changed);},[]);
  useEffect(()=>{refreshToken.current++;refreshingRef.current=false;setRefreshing(false);setLastSuccess(null);setRefreshError("");setOperationError("");setReceipt("");},[scopeKey]);
  useEffect(()=>{if(result && result.page!==page)setPage(result.page);},[result?.page,page]);
  useLayoutEffect(()=>{if(scroller.current){scroller.current.scrollTop=Number.isFinite(scroll?.top)?scroll?.top:0;scroller.current.scrollLeft=Number.isFinite(scroll?.left)?scroll?.left:0;}},[scroll?.top,scroll?.left,scopeKey]);
  const updateScrollCues = () => {
    const el=scroller.current, frame=viewportRoot.current;if(!el||!frame)return;
    const maxX=Math.max(0,el.scrollWidth-el.clientWidth),maxY=Math.max(0,el.scrollHeight-el.clientHeight);
    frame.dataset.left=String(el.scrollLeft>1);frame.dataset.right=String(maxX-el.scrollLeft>1);
    frame.dataset.top=String(el.scrollTop>1);frame.dataset.bottom=String(maxY-el.scrollTop>1);
    const header=el.querySelector("thead"), first=header?.querySelector("th"),last=el.querySelector(".data-list-with-actions thead th:last-child");
    frame.style.setProperty("--cue-left",`${first?.getBoundingClientRect().width||0}px`);
    frame.style.setProperty("--cue-right",`${last?.getBoundingClientRect().width||0}px`);
    frame.style.setProperty("--cue-top",`${header?.getBoundingClientRect().height||0}px`);
    frame.style.setProperty("--cue-scrollbar",`${Math.max(0,el.offsetHeight-el.clientHeight-2)}px`);
  };
  useLayoutEffect(()=>{
    const el=scroller.current;if(!el)return;updateScrollCues();
    const observer=new ResizeObserver(updateScrollCues);observer.observe(el);
    if(el.firstElementChild)observer.observe(el.firstElementChild);
    return()=>observer.disconnect();
  });
  const refresh = async () => {
    const current=latest.current;
    if(!current.onRefresh || current.paused || refreshingRef.current)return;
    refreshingRef.current=true;setRefreshing(true);setRefreshError("");
    const token=++refreshToken.current, startedScope=current.scopeKey;
    try {await current.onRefresh({scopeKey:startedScope,source:"LOCAL_STATE"});if(alive.current&&token===refreshToken.current&&latest.current.scopeKey===startedScope)setLastSuccess(new Date().toISOString());}
    catch(cause){if(alive.current&&token===refreshToken.current)setRefreshError(cause.message || "本地读取失败。");}
    finally{if(alive.current&&token===refreshToken.current){refreshingRef.current=false;setRefreshing(false);}}
  };
  const refreshIntervals=[3,5,10,15,30,60,300,600,900,1800];
  const seconds=refreshIntervals.includes(Number(interval))?Number(interval):0;
  useEffect(()=>{setCountdown(seconds);if(!seconds||!onRefresh||paused||refreshing)return;const deadline=Date.now()+seconds*1000;let fired=false;const timer=globalThis.setInterval(()=>{const left=Math.max(0,Math.ceil((deadline-Date.now())/1000));setCountdown(left);if(!left&&!fired){fired=true;refresh();}},250);return()=>clearInterval(timer);},[seconds,Boolean(onRefresh),scopeKey,paused,refreshing]);
  const operate = async callback => {setOperationError("");try{await callback();}catch(cause){setOperationError(cause.message || "操作失败。");}};
  const exportRows = async () => {
    if(exportingRef.current)return;
    exportingRef.current=true;setExporting(true);setOperationError("");setReceipt("");
    const current=latest.current, source=exportSource || viewId;
    try {
      if(!current.onExport || current.exportDisabledReason || current.error)throw Error(current.exportDisabledReason || "未获导出权限。");
      const prepared=queryList(current.rows,{...current});
      const payload=projectListExport(prepared.matchedRows,current.columns,{getRowId:current.getRowId,source,query:current.query,sort:prepared.sort,order:prepared.order});
      if(await current.onExport(structuredClone(payload))===false)throw Error("业务权限核对未通过，未生成导出文件。");
      if(!alive.current)return;
      const next=latest.current;
      if(next.scopeKey!==current.scopeKey || next.source!==current.source || next.onExport!==current.onExport || !next.onExport || next.exportDisabledReason || next.error)throw Error("授权范围已变化，请重新核对后导出。");
      const rechecked=queryList(next.rows,{...next});
      const updated=projectListExport(rechecked.matchedRows,next.columns,{getRowId:next.getRowId,source,query:next.query,sort:rechecked.sort,order:rechecked.order});
      if(JSON.stringify(updated)!==JSON.stringify(payload))throw Error("查询结果已变化，请重新导出当前结果。");
      const blob=new Blob([JSON.stringify({...payload,exportedAt:new Date().toISOString()},null,2)],{type:"application/json;charset=utf-8"});
      const url=URL.createObjectURL(blob),anchor=document.createElement("a");
      try {anchor.href=url;anchor.download="query-results.json";document.body.appendChild(anchor);anchor.click();}finally{anchor.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
      setReceipt(`已生成当前查询结果 JSON：${payload.count} 条记录；不是当前页，也不包含其他授权范围。`);
    } catch(cause){if(alive.current)setOperationError(cause.message || "导出失败，未生成文件。");}
    finally{exportingRef.current=false;if(alive.current)setExporting(false);}
  };
  const chooseSort = key => {const same=sort===key;setSort(same&&order==="desc"?"":key);setOrder(same&&order==="asc"?"desc":"asc");setPage(1);};
  const clear = () => operate(async()=>{await onClearFilters?.();setQuery("");setPage(1);});
  const sticky={position:"sticky",background:"var(--card)",borderBottom:"1px solid var(--border)",padding:".7rem .8rem",textAlign:"left"};
  return <section ref={listRoot} className={`resource-data-list${compact?" data-list-compact":""}`} aria-label={title} style={{minWidth:0,maxWidth:"100%"}}>
    <div className="data-list-controls">
    <div className="data-list-tools data-list-filters">
      <Field label="搜索当前授权列表" placeholder={searchPlaceholder || "搜索名称或关键词"} value={query} onChange={value=>{setQuery(value);setPage(1);}}/>
      {filters}
      <Button onClick={clear}>{onClearFilters?"清空条件":"清空搜索"}</Button>
    </div>
    <div className="data-list-tools data-list-refresh-tools">
      <div className="data-list-primary-actions">
      {onCreate&&<Button icon="Plus" onClick={()=>operate(onCreate)} disabled={Boolean(createDisabledReason)} title={createDisabledReason}>{createLabel}</Button>}
      {onImport&&<Button onClick={()=>operate(onImport)} disabled={Boolean(importDisabledReason)} title={importDisabledReason}>{importLabel}</Button>}
      {onExport&&<Button icon="Download" onClick={exportRows} disabled={Boolean(exportDisabledReason)||exporting||!result?.count||Boolean(error)} title={exportDisabledReason}>{exporting?"正在核对导出…":"导出当前查询结果"}</Button>}
      </div>
      {!renderCards&&availableColumns.length>1&&<details className="data-list-columns"><summary>显示列</summary><div className="data-list-column-options" aria-label="显示列设置">{availableColumns.map(column=><label key={column.key}><input type="checkbox" checked={visibleColumns.some(item=>item.key===column.key)} disabled={visibleColumns.length===1&&visibleColumns[0].key===column.key} onChange={event=>setColumnPreferences({...hiddenColumns,[column.key]:event.target.checked})}/>{column.label}</label>)}<Button onClick={()=>setColumnPreferences({})}>恢复默认列</Button></div></details>}
      <div className="list-refresh-split"><Button icon="RotateCcw" onClick={refresh} disabled={!onRefresh||Boolean(paused)||refreshing} title={paused}>{refreshing?"正在刷新…":"刷新"}</Button><Select indicator="ChevronDown" aria-label="定时刷新" value={String(seconds)} options={[{value:"0",label:"关闭定时刷新"},...refreshIntervals.map(value=>({value:String(value),label:`每 ${value<60?`${value} 秒`:`${value/60} 分钟`}刷新`}))]} disabled={!onRefresh} onChange={event=>setIntervalValue(event.target.value)}/></div>
      {!!seconds&&<span className="list-refresh-countdown" role="timer" aria-label={paused?`定时刷新暂停：${paused}`:`距下次刷新 ${countdown} 秒`}>{paused?`已暂停 · ${paused}`:refreshing?"刷新中…":`${countdown}s 后刷新`}</span>}
      {lastSuccess&&<small className="list-refresh-receipt" role="status">最近成功：{new Date(lastSuccess).toLocaleTimeString("zh-CN",{hour12:false})}</small>}
    </div>
    </div>
    {[error,queryError,refreshError,operationError].filter(Boolean).map((message,index)=><div key={index} role="alert"><Notice tone="warn">{typeof message === "string"?message:message.message || "操作失败。"}</Notice></div>)}
    {receipt&&<p role="status">{receipt}</p>}
    {result&&!error&&<>
      <p className="data-list-count" id={`${regionId}-count`} aria-live="polite">{compact?`共 ${result.count} 个文件${result.count!==result.authorizedCount?` · 此位置 ${result.authorizedCount} 个`:""}`:<>匹配 {result.count} 条 / 当前授权 {result.authorizedCount} 条</>}</p>
      <div className="data-list-viewport" ref={viewportRoot}>
      <div ref={scroller} role="region" tabIndex={0} aria-label={`${title}，可横向和纵向滚动`} aria-describedby={`${regionId}-count`} className="data-list-scroll" onScroll={event=>{updateScrollCues();setScroll({top:event.currentTarget.scrollTop,left:event.currentTarget.scrollLeft});}}>
        {renderCards ? <div className="file-cards">{result.rows.map(row=>renderCards(row))}{!result.count&&<div>{emptyText}</div>}</div> : <table className={renderActions?"data-list-with-actions":undefined}><caption style={{textAlign:"left",padding:".5rem .8rem",background:"var(--card)"}}>{title}</caption>
          <thead><tr><th scope="col" style={{...sticky,top:0,left:0,zIndex:4,width:"3rem",minWidth:"3rem"}}>序号</th>{visibleColumns.map(column=>{const sortable=column.sortable!==false&&typeof(column.sortValue||column.value)==="function";return <th key={column.key} scope="col" aria-sort={sortable?result.sort===column.key?result.order==="asc"?"ascending":"descending":"none":undefined} style={{...sticky,top:0,zIndex:2,minWidth:column.minWidth||(column===visibleColumns[0]?"16rem":"9rem")}}>{sortable?<button type="button" className="data-list-sort" onClick={()=>chooseSort(column.key)} aria-label={`按${column.label}排序${result.sort===column.key?result.order==="asc"?"，当前升序":"，当前降序":""}`}>{column.label} {result.sort===column.key?result.order==="asc"?"↑":"↓":"↕"}</button>:column.label}</th>})}{renderActions&&<th scope="col" style={{...sticky,top:0,right:0,zIndex:4}}>操作</th>}</tr></thead>
          <tbody>{result.rows.map((row,index)=><tr key={listRowId(row,getRowId)}><td style={{...sticky,left:0,zIndex:1}}>{index+1}</td>{visibleColumns.map(column=>{const value=column.value?.(row),content=column.render?column.render(row):value==null?"—":String(value);return <td key={column.key}><div className="data-list-cell" style={{maxWidth:column.maxWidth||"28rem"}}>{!column.render&&typeof value==="string"&&value.length>160?<details><summary>{value.slice(0,100)}… 查看全文</summary><div style={{whiteSpace:"pre-wrap"}}>{value}</div></details>:content}</div></td>})}{renderActions&&<td style={{...sticky,right:0,zIndex:1}}><div className="data-list-actions">{renderActions(row)}</div></td>}</tr>)}{!result.count&&<tr><td colSpan={visibleColumns.length+1+(renderActions?1:0)}><div>{emptyText}</div>{!compact&&<Button onClick={clear}>{onClearFilters?"清空条件":"清空搜索"}</Button>}</td></tr>}</tbody>
        </table>}
      </div>
        {["left","right","top","bottom"].map(edge=><span key={edge} aria-hidden="true" className={`data-list-shadow data-list-shadow-${edge}`}/>)}
      </div>
      <nav className="data-list-pagination" aria-label="列表分页">
        <strong className="data-list-total">共 {result.count} 条</strong>
        <Button disabled={result.page<=1} onClick={()=>setPage(result.page-1)}>上一页</Button><span>第 {result.page} / {result.pageCount} 页 · {result.count?`${result.offset+1}–${result.offset+result.rows.length}`:"0"} / {result.count} 条</span><Button disabled={result.page>=result.pageCount} onClick={()=>setPage(result.page+1)}>下一页</Button>
        <Field label="直接页码" type="number" min={1} max={result.pageCount} step={1} value={String(result.page)} onChange={value=>setPage(/^\d+$/.test(value)?Math.min(result.pageCount,Math.max(1,Number(value))):1)}/>
        <Field label="每页条数" value={String(result.pageSize)} options={pageSizes.map(size=>({value:String(size),label:`${size} 条`}))} onChange={value=>{setPageSize(Number(value));setPage(1);}}/>
      </nav>
    </>}
  </section>;
}
