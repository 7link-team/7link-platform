import { useRef, useState, type PointerEvent } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { packageSourceRows, type ClientPackageCommentInput } from "../../lib/clientwork-api"

export type PackageSourceSelection = Omit<ClientPackageCommentInput,"body">

export function PackageSourceViewer({text,name,kind,onPick}:{text:string;name:string;kind:NonNullable<ClientPackageCommentInput["anchor_kind"]>;onPick:(value:PackageSourceSelection)=>void}){
  const t=useTranslations("iam"),[lineStart,setLineStart]=useState<number|null>(null),[selected,setSelected]=useState("")
  const frame=useRef<HTMLIFrameElement>(null),surface=useRef<HTMLDivElement>(null)
  const [device,setDevice]=useState<"DESKTOP"|"TABLET"|"PHONE">("DESKTOP"),[region,setRegion]=useState<{x:number;y:number;w:number;h:number}|null>(null)
  const origin=useRef<{x:number;y:number}|null>(null)
  const pick=(key:string,value:PackageSourceSelection)=>{setSelected(key);onPick(value)}
  const source=text.replace(/\r\n/g,"\n")
  if(kind==="CODE_LINES")return <div role="group" aria-label={t("packageCommentKindCODE_LINES")} className="max-h-96 overflow-auto border font-mono text-sm" onPointerUp={()=>setLineStart(null)}>{source.split("\n").map((line,index)=><button type="button" key={index} data-source-line={index+1} aria-pressed={selected.split(":").map(Number).some((_,n,a)=>n===0&&index+1>=a[0]!&&index+1<=a[1]!)} className="flex min-h-11 w-full gap-3 whitespace-pre-wrap border-b px-3 text-left aria-pressed:bg-accent" onPointerDown={event=>{event.preventDefault();setLineStart(index+1);pick(`${index+1}:${index+1}`,{anchor_kind:kind,start_line:index+1,end_line:index+1})}} onPointerEnter={()=>{if(lineStart!==null){const start=Math.min(lineStart,index+1),end=Math.max(lineStart,index+1);pick(`${start}:${end}`,{anchor_kind:kind,start_line:start,end_line:end})}}} onClick={event=>{if(event.detail===0)pick(`${index+1}:${index+1}`,{anchor_kind:kind,start_line:index+1,end_line:index+1})}}><span aria-label={t("packageCommentStartLine")}>{index+1}</span><span>{line||" "}</span></button>)}</div>
  if(kind==="TABLE_ROW"){
    let rows:unknown[];try{rows=packageSourceRows(source,name)}catch{return <p role="alert">{t("packageCommentViewerUnavailable")}</p>}
    return <div className="max-h-96 overflow-auto"><table className="w-full text-left text-sm"><tbody>{rows.map((row,index)=><tr key={index} className="border-b"><td><button type="button" data-source-row={index} aria-pressed={selected===String(index)} className="min-h-11 w-full px-3 text-left aria-pressed:bg-accent" onClick={()=>pick(String(index),{anchor_kind:kind,row_index:index})}>{t("packageCommentRow")} {index}<span className="block whitespace-pre-wrap">{/\.json$/i.test(name)&&typeof row==="string"?row:JSON.stringify(row)}</span></button></td></tr>)}</tbody></table></div>
  }
  if(kind==="TEXT_PARAGRAPH")return <div role="group" aria-label={t("packageCommentKindTEXT_PARAGRAPH")} className="grid max-h-96 gap-2 overflow-auto">{source.trim().split(/\n[\t ]*\n/).map((paragraph,index)=><button type="button" key={index} data-source-paragraph={index} aria-pressed={selected===String(index)} className="min-h-11 whitespace-pre-wrap rounded border p-3 text-left aria-pressed:bg-accent" onClick={()=>pick(String(index),{anchor_kind:kind,paragraph_index:index,text_quote:Array.from(paragraph).slice(0,500).join("")})}>{paragraph}</button>)}</div>
  if(!/\.html?$/i.test(name))return <p role="alert">{t("packageCommentViewerUnavailable")}</p>
  if(typeof DOMParser==="undefined")return <p role="status">{t("loadingShort")}</p>
  const width={DESKTOP:960,TABLET:768,PHONE:390}[device]
  const document=new DOMParser().parseFromString(source,"text/html")
  document.querySelectorAll("script,iframe,object,embed,base,meta[http-equiv],form").forEach(node=>node.remove())
  document.querySelectorAll("*").forEach(node=>{for(const attr of Array.from(node.attributes))if(attr.name.startsWith("on")||["href","src","srcset","action","formaction"].includes(attr.name)&&!attr.value.startsWith("data:image/"))node.removeAttribute(attr.name)})
  const isolatedSource=document.documentElement.outerHTML
  const selectRegion=(event:PointerEvent<HTMLDivElement>)=>{
    const start=origin.current,box=surface.current?.getBoundingClientRect(),doc=frame.current?.contentDocument,win=frame.current?.contentWindow
    if(!start||!box||!doc||!win)return
    origin.current=null
    const end={x:Math.max(0,Math.min(box.width,event.clientX-box.left)),y:Math.max(0,Math.min(box.height,event.clientY-box.top))},rect={x:Math.min(start.x,end.x)+win.scrollX,y:Math.min(start.y,end.y)+win.scrollY,w:Math.abs(end.x-start.x),h:Math.abs(end.y-start.y)}
    if(rect.w<1||rect.h<1)return
    // Extract actual visible text intersecting the picked rectangle. No scripts
    // run in this same-origin sandbox; Range geometry is browser-owned.
    const quote:string[]=[],walker=doc.createTreeWalker(doc.body,NodeFilter.SHOW_TEXT)
    for(let node=walker.nextNode();node;node=walker.nextNode()){if(node.parentElement?.closest("script,style"))continue;const range=doc.createRange();range.selectNodeContents(node);if(Array.from(range.getClientRects()).some(r=>r.right>=rect.x-win.scrollX&&r.left<=rect.x-win.scrollX+rect.w&&r.bottom>=rect.y-win.scrollY&&r.top<=rect.y-win.scrollY+rect.h))quote.push(node.textContent??"")}
    const text_quote=Array.from(quote.join(" ").replace(/\s+/g," ").trim()).slice(0,200).join("");if(!text_quote)return
    setRegion({...rect,x:rect.x-win.scrollX,y:rect.y-win.scrollY});pick("region",{anchor_kind:kind,text_quote,rect,viewport:{w:box.width,h:box.height,dpr:window.devicePixelRatio,device},scroll:{x:win.scrollX,y:win.scrollY}})
  }
  return <div className="grid gap-2"><p className="text-sm">{t("packageCommentRectOnly")}</p><label>{t("packageCommentDevice")}<select className="ml-2 min-h-11 rounded border bg-background px-2" value={device} onChange={event=>{setDevice(event.target.value as typeof device);setRegion(null);onPick({})}}>{(["DESKTOP","TABLET","PHONE"] as const).map(value=><option key={value} value={value}>{t(`packageCommentDevice${value}`)}</option>)}</select></label><div className="max-w-full overflow-auto"><div ref={surface} className="relative h-96" style={{width}}>
    <iframe ref={frame} title={t("packageCommentKindHTML_REGION")} sandbox="allow-same-origin" className="h-full w-full border" srcDoc={`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; base-uri 'none'"><style>body{overflow:auto}</style>${isolatedSource}`}/>
    <div role="button" tabIndex={0} aria-label={t("packageCommentPickRegion")} data-source-region className="absolute inset-0 cursor-crosshair touch-none" onPointerDown={event=>{const box=event.currentTarget.getBoundingClientRect();origin.current={x:event.clientX-box.left,y:event.clientY-box.top};event.currentTarget.setPointerCapture(event.pointerId)}} onPointerUp={selectRegion} onPointerCancel={()=>{origin.current=null}} onWheel={event=>{frame.current?.contentWindow?.scrollBy(event.deltaX,event.deltaY);onPick({});setRegion(null)}} onKeyDown={event=>{if(event.key==="Enter"||event.key===" "){const box=event.currentTarget.getBoundingClientRect();origin.current={x:0,y:0};selectRegion({clientX:box.right,clientY:box.bottom} as PointerEvent<HTMLDivElement>);event.preventDefault()}}}/>
    {region&&<div className="pointer-events-none absolute border-2 border-primary bg-primary/10" style={{left:region.x,top:region.y,width:region.w,height:region.h}}/>}
  </div></div></div>
}
