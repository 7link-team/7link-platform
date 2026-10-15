import React, { useEffect, useRef, useState } from "react";
import { Icon, IconButton, Button, Modal, Select, Notice, Empty } from "./ui";
import { chatEmoji, chatLinks, safeChatUrl, chatFileText, chatFileBlob, normalizeChatAttachments, readChatFile } from "./chat-content";

function Inline({ text, onUrl }) {
  const parts=text.split(/(`[^`\n]+`|\*\*[^*\n]+\*\*|\*[^*\n]+\*|~~[^~\n]+~~|\[[^\]\n]+\]\([^\s)]+\)|https?:\/\/[^\s<>]+)/g);
  return parts.map((part,index)=>{
    if(part.startsWith("`") && part.endsWith("`")) return <code key={index}>{part.slice(1,-1)}</code>;
    if(part.startsWith("**") && part.endsWith("**")) return <strong key={index}>{part.slice(2,-2)}</strong>;
    if(part.startsWith("*") && part.endsWith("*")) return <em key={index}>{part.slice(1,-1)}</em>;
    if(part.startsWith("~~") && part.endsWith("~~")) return <del key={index}>{part.slice(2,-2)}</del>;
    const link=part.match(/^\[([^\]]+)\]\(([^)]+)\)$/), url=safeChatUrl(link?.[2] || part);
    if(url) return <a key={index} href={url} target="_blank" rel="noopener noreferrer" onClick={event=>{if(onUrl && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey){event.preventDefault();onUrl(url);}}}>{link?.[1] || part}</a>;
    return part;
  });
}
export function ChatMarkdown({ text = "", onUrl }) {
  // Common chat Markdown only; raw HTML and executable URL schemes remain inert text.
  const lines=text.split("\n"), blocks=[];
  for(let i=0;i<lines.length;i++) {
    const line=lines[i];
    if(line.startsWith("```")) {
      const language=line.slice(3).trim(), code=[];while(++i<lines.length && !lines[i].startsWith("```"))code.push(lines[i]);
      blocks.push(<div className="chat-code" key={i}><small>{language || "代码"}</small><pre><code>{code.join("\n")}</code></pre></div>);continue;
    }
    if(/^#{1,3} /.test(line)) {blocks.push(<p className="chat-markdown-heading" key={i}><Inline text={line.replace(/^#{1,3} /,"")} onUrl={onUrl} /></p>);continue;}
    if(/^> ?/.test(line)) {blocks.push(<blockquote key={i}><Inline text={line.replace(/^> ?/,"")} onUrl={onUrl} /></blockquote>);continue;}
    if(/^([-*] |\d+\. )/.test(line)) {blocks.push(<div className="chat-list-line" key={i}><span aria-hidden="true">•</span><Inline text={line.replace(/^([-*] |\d+\. )/,"")} onUrl={onUrl} /></div>);continue;}
    if(line.includes("|") && /^\s*\|?\s*:?-{3,}/.test(lines[i+1] || "")) {
      const cells=value=>value.replace(/^\s*\||\|\s*$/g,"").split("|").map(cell=>cell.trim()),head=cells(line),rows=[];i++;
      while(lines[i+1]?.includes("|"))rows.push(cells(lines[++i]));
      blocks.push(<div className="chat-table-scroll" key={i}><table><thead><tr>{head.map((cell,j)=><th key={j}><Inline text={cell} onUrl={onUrl} /></th>)}</tr></thead><tbody>{rows.map((row,k)=><tr key={k}>{row.map((cell,j)=><td key={j}><Inline text={cell} onUrl={onUrl} /></td>)}</tr>)}</tbody></table></div>);continue;
    }
    blocks.push(line ? <p key={i}><Inline text={line} onUrl={onUrl} /></p> : <br key={i} />);
  }
  return <div className="chat-markdown">{blocks}</div>;
}

function MediaPlayer({ file }) {
  const ref=useRef(null),[playing,setPlaying]=useState(false),[time,setTime]=useState(0),[duration,setDuration]=useState(0),[error,setError]=useState("");
  const video=file.kind === "video",format=value=>`${Math.floor(value/60)}:${String(Math.floor(value%60)).padStart(2,"0")}`;
  const props={ref,src:file.data,preload:"metadata",onLoadedMetadata:event=>setDuration(Number.isFinite(event.currentTarget.duration)?event.currentTarget.duration:0),onTimeUpdate:event=>setTime(event.currentTarget.currentTime),onPlay:()=>setPlaying(true),onPause:()=>setPlaying(false),onEnded:()=>setPlaying(false),onError:()=>setError("无法播放此编码，原文件仍可下载。")};
  return <div className="chat-media-player">
    {video ? <video {...props} aria-label={file.name} playsInline /> : <audio {...props} aria-label={file.name} />}
    <div className="chat-media-controls">
      <IconButton name={playing ? "Pause" : "Play"} label={playing ? "暂停媒体" : "播放媒体"} onClick={async()=>{try{if(playing)ref.current.pause();else await ref.current.play();setError("");}catch{setError("无法播放此编码，原文件仍可下载。");}}} />
      <input type="range" aria-label="媒体播放进度" min={0} max={duration || 1} step={0.1} value={Math.min(time,duration || 1)} disabled={!duration} onChange={event=>{ref.current.currentTime=Number(event.target.value);setTime(Number(event.target.value));}} />
      <small>{format(time)} / {format(duration)}</small>
    </div>
    {error && <p role="alert">{error}</p>}
    {file.kind === "audio" && <small className="muted">原始语音 · 未转写；转写服务待接入</small>}
  </div>;
}

function CsvTable({ text, delimiter }) {
  const rows=[];let row=[],cell="",quoted=false;
  for(let i=0;i<text.length && rows.length<100;i++) {const char=text[i];if(char==='"'){if(quoted&&text[i+1]==='"'){cell+='"';i++;}else quoted=!quoted;}else if(!quoted&&(char===delimiter||char==='\n')){row.push(cell);cell="";if(char==='\n'){rows.push(row);row=[];}}else if(char!=='\r')cell+=char;}
  if(rows.length<100 && (cell || row.length)){row.push(cell);rows.push(row);}
  return <div className="chat-table-scroll"><table><tbody>{rows.map((row,i)=><tr key={i}>{row.slice(0,30).map((cell,j)=><td key={j}>{cell}</td>)}</tr>)}</tbody></table><small>原件预览 · 最多显示前 100 行 / 30 列</small></div>;
}
const fileIcons={image:"Image",audio:"Mic",video:"Clapperboard",pdf:"FileText",document:"FileText",spreadsheet:"Table2",slides:"Presentation",markdown:"FileText",code:"FileCode2",table:"Table2",text:"FileText",file:"Paperclip"};
export function FilePreview({ file, onUrl }) {
  const [blobUrl,setBlobUrl]=useState("");
  useEffect(()=>{if(file.kind!=="pdf" || !file.data)return;const url=URL.createObjectURL(chatFileBlob(file));setBlobUrl(url);return()=>URL.revokeObjectURL(url);},[file.id,file.data,file.kind]);
  if(!file.data) return <Empty title="原文件尚未连接" text="此历史消息只有文件名引用。原件接入后可预览；当前没有补造文件内容。" />;
  if(file.kind==="image") return <img className="chat-full-image" src={file.data} alt={file.name} />;
  if(["audio","video"].includes(file.kind)) return <MediaPlayer file={file} />;
  if(file.kind==="pdf") return blobUrl ? <iframe className="chat-pdf-frame" title={`PDF · ${file.name}`} src={blobUrl} /> : <p role="status">正在读取 PDF 原件…</p>;
  if(file.kind==="markdown")return <ChatMarkdown text={chatFileText(file)} onUrl={onUrl} />;
  if(file.kind==="table")return <CsvTable text={chatFileText(file)} delimiter={file.name.endsWith(".tsv")?"\t":","} />;
  if(["code","text"].includes(file.kind))return <pre className="chat-file-text">{chatFileText(file)}</pre>;
  return <Empty title={['document','spreadsheet','slides'].includes(file.kind) ? "Office 预览尚未生成" : "此类型暂不支持内嵌预览"} text="真实原文件已保存在本地原型中，可下载。Office 渲染与缩略图服务待接入，失败不丢失原件。" />;
}
function DownloadFile({ file }) {
  if(!file.data)return null;
  return <Button icon="Download" onClick={()=>{const url=URL.createObjectURL(chatFileBlob({...file,kind:"file",mime:"application/octet-stream"}));const anchor=document.createElement("a");anchor.href=url;anchor.download=file.name;anchor.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}}>下载原文件</Button>;
}
export function ChatMessageContent({ message }) {
  const [selected,setSelected]=useState(null),[refresh,setRefresh]=useState(0),[imageErrors,setImageErrors]=useState({});
  const files=message.attachments?.length ? message.attachments : message.file ? [{id:"legacy",name:message.file,kind:"file"}] : [];
  if(message.recalled)return <p>此消息已撤回，原记录依审计政策保留。</p>;
  const onUrl=url=>setSelected({kind:"url",url,name:new URL(url).hostname});
  return <>
    <ChatMarkdown text={message.text} onUrl={onUrl} />
    <div className="chat-message-materials">
      {files.map(file=><div key={file.id} className={`chat-file-card chat-file-${file.kind}`}>
        {file.kind==="image" && file.data && !imageErrors[file.id] ? <button type="button" className="chat-image-button" aria-label={`预览 ${file.name}`} onClick={()=>setSelected(file)}><img loading="lazy" src={file.data} alt={file.name} onError={()=>setImageErrors(old=>({...old,[file.id]:true}))} /></button> : file.data && ["audio","video"].includes(file.kind) ? <MediaPlayer file={file} /> : null}
        <button type="button" className="chat-file-heading" onClick={()=>setSelected(file)} aria-label={`预览 ${file.name}`}><Icon name={fileIcons[file.kind] || "Paperclip"} /><span><b>{file.name}</b><small>{file.size == null ? "历史文件引用 · 原件未连接" : `${(file.size/1024).toFixed(1)} KB · 本地原件`}{["document","spreadsheet","slides"].includes(file.kind) ? " · 预览待生成" : ""}</small></span><Icon name="Maximize2" /></button>
      </div>)}
      {chatLinks(message.text).map(url=><div key={url} className="chat-url-card"><Icon name="Globe2" /><div><b>{new URL(url).hostname}</b><p>{url}</p><small>网址卡片 · 未抓取站点元数据</small><div className="actions"><Button onClick={()=>onUrl(url)}>当前页预览</Button><a className="btn" href={url} target="_blank" rel="noopener noreferrer"><Icon name="ArrowUpRight" />新标签页打开</a></div></div></div>)}
    </div>
    {selected && <Modal title={selected.name} className="chat-material-dialog" wide onClose={()=>setSelected(null)}>
      <div className="chat-material-toolbar">{selected.kind === "url" ? <><IconButton name="RotateCcw" label="刷新站点预览" onClick={()=>setRefresh(value=>value+1)} /><input aria-label="预览站点地址" readOnly value={selected.url} /><a className="btn" href={selected.url} target="_blank" rel="noopener noreferrer">新标签页打开</a></> : <><span>{selected.source === "LOCAL_FILE" ? "本地原件" : "历史引用"}</span><DownloadFile file={selected} /></>}</div>
      <div className="chat-material-body">{selected.kind === "url" ? <><Notice>站点可能禁止嵌入或需要登录；可选择新标签页打开。此预览不代表 FRP 已连接。</Notice><iframe key={refresh} title={`站点预览 · ${selected.name}`} src={selected.url} sandbox="allow-scripts allow-forms allow-popups" referrerPolicy="no-referrer" /></> : <FilePreview file={selected} onUrl={onUrl} />}</div>
    </Modal>}
  </>;
}

export function EmojiPicker({ title = "选择表情", onSelect, onClose }) {
  return <Modal title={title} onClose={onClose}><div className="chat-emoji-grid">{chatEmoji.map(emoji=><button type="button" key={emoji} aria-label={`选择表情 ${emoji}`} onClick={()=>{onSelect(emoji);onClose();}}>{emoji}</button>)}</div></Modal>;
}

export function ChatComposerTools({ value, onChange, inputRef, files, onFiles, contextKey, toast, disabled, onBusy }) {
  const [picker,setPicker]=useState(false),[busy,setBusy]=useState(false),[voice,setVoice]=useState(false),[recording,setRecording]=useState(false),[seconds,setSeconds]=useState(0),[voiceError,setVoiceError]=useState("");
  const generation=useRef(0),recorder=useRef(null),stream=useRef(null),timer=useRef(null),capture=useRef(null);
  const cleanup=()=>{clearInterval(timer.current);timer.current=null;stream.current?.getTracks().forEach(track=>track.stop());stream.current=null;};
  useEffect(()=>{generation.current++;setPicker(false);setVoice(false);setRecording(false);setBusy(false);return()=>{generation.current++;if(recorder.current?.state==="recording")recorder.current.stop();cleanup();};},[contextKey,disabled]);
  useEffect(()=>{onBusy(busy || recording);},[busy,recording,onBusy]);
  useEffect(()=>{const hide=()=>{if(document.hidden && recorder.current?.state==="recording")recorder.current.stop();};document.addEventListener("visibilitychange",hide);return()=>document.removeEventListener("visibilitychange",hide);},[]);
  const addFiles=async list=>{const current=generation.current;setBusy(true);try{const added=await Promise.all([...list].map(readChatFile));if(current!==generation.current)return;onFiles(normalizeChatAttachments([...files,...added]));}catch(error){if(current===generation.current)toast(error.message,true);}finally{if(current===generation.current)setBusy(false);}};
  const insert=text=>{const input=inputRef.current,start=input?.selectionStart??value.length,end=input?.selectionEnd??start;onChange(value.slice(0,start)+text+value.slice(end));requestAnimationFrame(()=>{input?.focus();input?.setSelectionRange(start+text.length,start+text.length);});};
  const startRecording=async()=>{
    if(!navigator.mediaDevices?.getUserMedia || !window.MediaRecorder){setVoiceError("此浏览器不能录音，可选择音频文件发送。");return;}
    const current=generation.current;
    try{const media=await navigator.mediaDevices.getUserMedia({audio:true});if(current!==generation.current){media.getTracks().forEach(track=>track.stop());return;}
      stream.current=media;const chunks=[],started=Date.now(),instance=new MediaRecorder(media);recorder.current=instance;capture.current={generation:current,discard:false};
      instance.ondataavailable=event=>{if(event.data.size)chunks.push(event.data);};
      instance.onstop=async()=>{media.getTracks().forEach(track=>track.stop());if(current!==generation.current || capture.current?.discard)return;cleanup();setRecording(false);const blob=new Blob(chunks,{type:instance.mimeType || "audio/webm"});const ext=blob.type.includes("mp4")?"m4a":blob.type.includes("ogg")?"ogg":"webm";await addFiles([new File([blob],`语音-${new Date().toISOString().replace(/[:.]/g,"-")}.${ext}`,{type:blob.type.split(";")[0]})]);setVoice(false);};
      instance.start();setSeconds(0);setRecording(true);setVoiceError("");timer.current=setInterval(()=>{setSeconds(Math.floor((Date.now()-started)/1000));if(Date.now()-started>=60000&&instance.state==="recording")instance.stop();},250);
    }catch{setVoiceError("无法使用麦克风，请检查浏览器授权或选择音频文件。");cleanup();}
  };
  const closeVoice=()=>{generation.current++;if(capture.current)capture.current.discard=true;if(recorder.current?.state==="recording")recorder.current.stop();cleanup();setRecording(false);setBusy(false);setVoice(false);};
  return <>
    <div className="chat-format-tools" role="group" aria-label="消息格式与材料">
      <label className="attach-control" title="添加图片、文件、音视频"><Icon name="Paperclip" /><input type="file" multiple aria-label="添加消息附件" disabled={disabled||busy} onChange={event=>{addFiles(event.target.files);event.target.value="";}} /></label>
      <IconButton name="Smile" label="添加表情" disabled={disabled} onClick={()=>setPicker(true)} />
      <IconButton name="Mic" label="语音消息" disabled={disabled||busy} onClick={()=>{setVoiceError("");setVoice(true);}} />
      <button type="button" aria-label="插入粗体" disabled={disabled} onClick={()=>insert("**重点**")}>B</button>
      <button type="button" aria-label="插入斜体" disabled={disabled} onClick={()=>insert("*强调*")}>I</button>
      <IconButton name="Code2" label="插入代码块" disabled={disabled} onClick={()=>insert("\n```text\n代码内容\n```\n")} />
      <small>{busy ? "正在读取原件…" : "Markdown · ⌘ / Ctrl + Enter 发送"}</small>
    </div>
    {files.length>0 && <div className="chat-pending-files">{files.map(file=><div key={file.id}><Icon name={fileIcons[file.kind]} /><span>{file.name}</span><IconButton name="X" label={`移除待发送附件 ${file.name}`} disabled={disabled||busy} onClick={()=>onFiles(files.filter(item=>item.id!==file.id))} /></div>)}</div>}
    {picker && <EmojiPicker onSelect={emoji=>insert(emoji)} onClose={()=>setPicker(false)} />}
    {voice && <Modal title="语音消息" onClose={closeVoice}><p>在当前浏览器录制原始音频，最长 60 秒；发送前可移除。音频只保存在本地原型，不自动转写。</p><p role="status">{recording ? `正在录音 · ${seconds} 秒` : "尚未开始录音"}</p>{voiceError && <p role="alert">{voiceError}</p>}<div className="actions">{recording ? <Button onClick={()=>recorder.current?.stop()}>停止并添加语音</Button> : <Button variant="primary" disabled={busy} onClick={startRecording}>开始录音</Button>}<label className="btn">选择音频文件<input className="sr-only" type="file" accept="audio/*" aria-label="选择语音文件" disabled={recording||busy} onChange={async event=>{await addFiles(event.target.files);setVoice(false);}} /></label></div></Modal>}
  </>;
}
