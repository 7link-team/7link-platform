// Local prototype files retain their actual bytes; no upload, conversion or transcription is claimed.
export const chatEmoji = ["😀", "😊", "😂", "😍", "🤔", "😅", "😢", "🎉", "👍", "👎", "❤️", "✅", "👀", "🙏", "🚀", "🔥", "💡", "🤝", "👏", "🎯", "💪", "⏳", "📌", "✨"];
export const maxChatFileBytes = 1024 * 1024;
export function safeChatUrl(value) {
  try { const url=new URL(value); return ["http:","https:"].includes(url.protocol) && !url.username && !url.password ? url.href : null; } catch { return null; }
}
export function chatFileKind(name, mime = "") {
  const ext=name.toLowerCase().split(".").at(-1);
  if (/^image\/(png|jpeg|gif|webp|avif)$/.test(mime)) return "image";
  if (/^audio\//.test(mime)) return "audio";
  if (/^video\//.test(mime)) return "video";
  if (ext === "pdf") return "pdf";
  if (["doc","docx","odt"].includes(ext)) return "document";
  if (["xls","xlsx","ods"].includes(ext)) return "spreadsheet";
  if (["ppt","pptx","odp"].includes(ext)) return "slides";
  if (["md","markdown"].includes(ext)) return "markdown";
  if (ext === "csv" || ext === "tsv") return "table";
  if (["js","jsx","ts","tsx","go","py","rs","java","sh","css","html","svg","json","yaml","yml","toml","xml","sql"].includes(ext)) return "code";
  if (mime.startsWith("text/") || ["txt","log"].includes(ext)) return "text";
  return "file";
}
export function normalizeChatAttachments(input = []) {
  if (!Array.isArray(input) || input.length > 6) throw Error("每条消息最多添加 6 个文件。");
  const ids=new Set();let total=0;
  return input.map(file => {
    if (!file || typeof file.id !== "string" || ids.has(file.id) || typeof file.name !== "string" || !file.name.trim() || file.name.length > 180 || !Number.isSafeInteger(file.size) || file.size < 0 || file.size > maxChatFileBytes) throw Error("附件格式无效；原型每个文件最多 1 MB。");
    ids.add(file.id); total+=file.size;
    if(total > 3*maxChatFileBytes) throw Error("每条消息附件总量最多 3 MB。");
    if (typeof file.mime !== "string" || !/^[a-z0-9][a-z0-9.+-]*\/[a-z0-9][a-z0-9.+-]*$/i.test(file.mime) || typeof file.data !== "string" || !file.data.startsWith(`data:${file.mime};base64,`)) throw Error("附件原件未读取完成，请重新选择。");
    const base64=file.data.slice(file.data.indexOf(",")+1);
    if (base64.length > Math.ceil(maxChatFileBytes/3)*4 || !/^[A-Za-z0-9+/]*={0,2}$/.test(base64)) throw Error("附件原件格式无效。");
    let bytes;try{bytes=atob(base64);}catch{throw Error("附件原件格式无效。");}
    if(bytes.length!==file.size) throw Error("附件大小与原件不一致。");
    return {id:file.id,name:file.name.trim(),size:file.size,mime:file.mime,data:file.data,kind:chatFileKind(file.name,file.mime),source:"LOCAL_FILE"};
  });
}
export function chatFileText(file) {
  if (!file?.data) return "";
  try { return new TextDecoder().decode(Uint8Array.from(atob(file.data.split(",")[1]), char=>char.charCodeAt(0))); } catch { return ""; }
}
export function chatLinks(text = "") {
  return [...new Set((text.match(/https?:\/\/[^\s<>]+/g) || []).map(value=>safeChatUrl(value.replace(/[)\]。。，,;；!?！？]+$/g,""))).filter(Boolean))].slice(0,6);
}
export function readChatFile(file) {
  if (file.size > maxChatFileBytes) return Promise.reject(Error(`${file.name} 超过原型 1 MB 文件上限；真实大文件上传待接入。`));
  return new Promise((resolve,reject)=>{
    const reader=new FileReader();reader.onerror=()=>reject(Error(`无法读取 ${file.name}，请重新选择。`));
    reader.onload=()=>{
      const mime=file.type || (file.name.toLowerCase().endsWith(".pdf") ? "application/pdf" : "application/octet-stream");
      const data=String(reader.result).replace(/^data:[^;]*;base64,/,`data:${mime};base64,`);
      try{resolve(normalizeChatAttachments([{id:crypto.randomUUID(),name:file.name,size:file.size,mime,data}])[0]);}catch(error){reject(error);}
    };reader.readAsDataURL(file);
  });
}
export function chatFileBlob(file) {
  const bytes=Uint8Array.from(atob(file.data.split(",")[1]),char=>char.charCodeAt(0));
  return new Blob([bytes],{type:file.kind === "pdf" ? "application/pdf" : file.mime});
}
