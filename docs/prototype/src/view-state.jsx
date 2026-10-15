import React, { createContext, useContext, useLayoutEffect, useRef, useState } from "react";

// View coordinates are separate from business state and never grant access.
export const ViewContext = createContext(null);
const scrollSelectors = ["main", ".messages", ".org-directory-list", ".org-detail-body", ".preview-tablist", ".channel-list", ".node-panel", ".chat-table-scroll"];
export function captureScroll() {
  return Object.fromEntries(scrollSelectors.flatMap(selector => [...document.querySelectorAll(selector)].map((element,index) => [`${selector}|${index}`, {top:element.scrollTop,left:element.scrollLeft}])));
}
export function restoreScroll(positions = {}) {
  for (const [key,position] of Object.entries(positions)) {
    const [selector,index="0"] = key.split("|");
    if(!scrollSelectors.includes(selector) || !/^\d+$/.test(index))continue;
    const element = document.querySelectorAll(selector)[Number(index)];
    if (element && Number.isFinite(position?.top) && Number.isFinite(position?.left)) {
      element.scrollTop = Math.max(0,position.top); element.scrollLeft = Math.max(0,position.left);
    }
  }
}
export function viewKey(full, tenantId, role, project) {
  const [path,query=""] = full.split("?"); const params=new URLSearchParams(query);
  for (const key of [...params.keys()]) if (key.startsWith("state.") || ["tab","view","segment","scroll","tenant","scope"].includes(key)) params.delete(key);
  params.sort(); return JSON.stringify([tenantId,role,project,path,params.toString()]);
}
function readSnapshot(key) { try {return JSON.parse(sessionStorage.getItem(`prototype-view:${key}`)) || {};} catch {return {};} }
export function saveViewSnapshot(key, patch) {
  try {sessionStorage.setItem(`prototype-view:${key}`,JSON.stringify({...readSnapshot(key),...patch}));} catch {/* Reading and navigation still work when view storage is unavailable. */}
}
export const savedView = key => readSnapshot(key);
export function syncViewURL(context) {
  const href=viewLink(context),url=new URL(href,location.href).href;
  history.replaceState({...history.state,prototypeReturn:{url,full:href.slice(1),tenantId:context.tenantId,role:context.role,scrolls:captureScroll(),windowX:scrollX,windowY:scrollY}},"",href);
}
export function useViewState(name, initial) {
  const context=useContext(ViewContext), initialRef=useRef(initial);
  const fallback=()=>typeof initialRef.current==="function" ? initialRef.current() : initialRef.current;
  const valid=candidate=>{
    const defaultValue=fallback();
    return defaultValue===null ? candidate===null || (candidate && typeof candidate==="object" && !Array.isArray(candidate)) : typeof candidate===typeof defaultValue;
  };
  const resolve=()=>{
    const value=context?.params.get(`state.${name}`);
    if(value!==undefined && value!==null) {try {const parsed=JSON.parse(value);return valid(parsed)?parsed:fallback();}catch {return fallback();}}
    const saved=context && readSnapshot(context.key).values;
    return saved && Object.hasOwn(saved,name) && valid(saved[name]) ? saved[name] : fallback();
  };
  const [value,setValue]=useState(resolve);
  const identity=`${name}:${context?.key}:${context?.params.get(`state.${name}`) || ""}`;
  useLayoutEffect(()=>{setValue(resolve());},[identity]);
  const latest=useRef(value);latest.current=value;
  const change=next=>{
    const actual=typeof next==="function" ? next(latest.current) : next;
    latest.current=actual;
    setValue(actual);
    if(context) {
      saveViewSnapshot(context.key,{values:{...(readSnapshot(context.key).values || {}),[name]:actual}});
      context.sync?.();
    }
  };
  return [value,change];
}
export function viewLink(context, patch = {}, positions = captureScroll()) {
  if(!context)return "";
  const [path,query=""]=context.full.split("?");const params=new URLSearchParams(query);
  params.set("tenant",context.tenantId);params.set("scope",context.project);
  for(const [name,value] of Object.entries(readSnapshot(context.key).values || {})) params.set(`state.${name}`,JSON.stringify(value));
  for(const [name,value] of Object.entries(patch)) {if(value===null)params.delete(name);else params.set(name,String(value));}
  if(!Object.hasOwn(patch,"segment")) {params.delete("segment");params.set("scroll",JSON.stringify(positions));} else params.delete("scroll");
  return `#${path}?${params}`;
}
export function SectionLink({ title }) {
  const context=useContext(ViewContext);
  if(!context || typeof title!=="string")return null;
  return <a className="section-anchor" aria-label={`直达 ${title}`} title="此段落的直接链接" href={viewLink(context,{segment:title})}>#</a>;
}
export function CurrentViewLink({ toast }) {
  const context=useContext(ViewContext);
  return <button type="button" className="current-view-link" aria-label="复制当前位置链接" onClick={async()=>{
    const href=new URL(viewLink(context),location.href).href;
    try {await navigator.clipboard.writeText(href);toast("位置链接已复制，包含页面、栏目与滚动位置");}
    catch {context.showLink(href);}
  }}>复制位置链接</button>;
}
export function Breadcrumbs({ items }) {
  return <nav className="breadcrumbs" aria-label="面包屑"><ol>{items.filter(Boolean).map((item,index,array)=><li key={`${index}:${item.label}`}>
    {index>0 && <span aria-hidden="true">/</span>}{index===array.length-1 ? <span aria-current="page">{item.label}</span> : <a href={`#${item.to}`}>{item.label}</a>}
  </li>)}</ol></nav>;
}
