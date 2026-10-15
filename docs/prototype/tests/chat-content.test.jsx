import { test, expect, beforeEach } from "bun:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { normalizeChatAttachments, chatFileText, safeChatUrl, chatLinks, chatFileKind, chatEmoji } from "../src/chat-content";
import { ChatMarkdown } from "../src/chat-rich";
const storage=new Map();Object.defineProperty(globalThis,"localStorage",{configurable:true,value:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)}});
const api=await import("../src/store");beforeEach(()=>api.reset());
const file=(patch={})=>({id:"fixture",name:"README.md",mime:"text/plain",size:6,data:"data:text/plain;base64,IyBoaQoh",...patch});
const atomic=fn=>{const before=structuredClone(api.read()),saved=[...storage];expect(fn).toThrow();expect(api.read()).toEqual(before);expect([...storage]).toEqual(saved);};
test("local attachment bytes, size, type and fixed source survive send/edit/reload without conversion claims",()=>{
 const input=file();const message=api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",text:"File source",attachments:[input]});
 expect(message.attachments[0]).toMatchObject({kind:"markdown",source:"LOCAL_FILE",size:6});expect(chatFileText(message.attachments[0])).toBe("# hi\n!");
 api.changeChannelMessage({tenantId:api.read().activeTenant,messageId:message.id,expectedText:message.text,action:"edit",text:"Changed description"});
 expect(api.read().messages.find(m=>m.id===message.id).attachments).toEqual(message.attachments);expect(JSON.parse(storage.get("collaboration-prototype-v1")).messages.find(m=>m.id===message.id).attachments[0].data).toBe(input.data);
});
test("all malformed attachment families fail atomically without partial messages or bytes",()=>{
 const cases=[{size:7},{data:"data:text/html;base64,IyBoaQoh"},{data:"data:text/plain;base64,!!!!!!"},{mime:"text/plain;bad"},{size:1024*1024+1},{name:""}];
 for(const patch of cases)atomic(()=>api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",attachments:[file(patch)]}));
 atomic(()=>api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",attachments:[file(),file()]}));
 expect(normalizeChatAttachments([])).toEqual([]);
});
test("attachment-only messages are valid; external/private and offline boundaries are identical to text",()=>{
 const message=api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",attachments:[file()]});expect(message.text).toBe("");expect(message.file).toBe("README.md");
 api.update(s=>s.offline=true);atomic(()=>api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",attachments:[file()]}));
 api.update(s=>{s.offline=false;s.role="external";});atomic(()=>api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"private",attachments:[file()]}));
});
test("emoji reactions toggle one current principal, keep different principals and reject unknown schemes/emoji",()=>{
 const message=api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"external",text:"Reaction source"}),input={tenantId:api.read().activeTenant,messageId:message.id,expectedText:message.text,action:"emoji",emoji:"🎉"};
 api.changeChannelMessage(input);api.update(s=>s.role="external");api.changeChannelMessage(input);
 expect(api.read().messages.find(m=>m.id===message.id).emojiReactions[0].principalIds).toEqual(["H-lin","external-demo"]);api.changeChannelMessage(input);expect(api.read().messages.find(m=>m.id===message.id).emojiReactions[0].principalIds).toEqual(["H-lin"]);
 atomic(()=>api.changeChannelMessage({...input,emoji:"not an emoji"}));expect(chatEmoji).toContain("🎉");
});
test("Markdown preserves content and only produces inert raw HTML and safe external links",()=>{
 const html=renderToStaticMarkup(<ChatMarkdown text={'**Important**\n```js\n<script>alert(1)</script>\n```\n<img src=x onerror=alert(1)>\n[bad](javascript:alert)\n[good](https://example.com)\n| A | B |\n| --- | --- |\n| 1 | 2 |'} />);
 expect(html).toContain("<strong>Important</strong>");expect(html).toContain("&lt;script&gt;");expect(html).not.toContain("<script>");expect(html).not.toContain('href="javascript:');expect(html).toContain('target="_blank"');expect(html).toContain('rel="noopener noreferrer"');expect(html).toContain("<table>");
 for(const url of ["javascript:alert(1)","data:text/html,hello","file:///tmp/a","https://u:p@example.com"])expect(safeChatUrl(url)).toBeNull();
 expect(chatLinks("https://example.com https://example.com")).toEqual(["https://example.com/"]);expect(chatFileKind("script.html","text/html")).toBe("code");expect(chatFileKind("budget.xlsx","application/octet-stream")).toBe("spreadsheet");
});
test("recall keeps canonical file bytes but hides further reactions and leaves an audit tombstone",()=>{
 const message=api.postChannelMessage({tenantId:api.read().activeTenant,channelId:"launch",attachments:[file()]});api.changeChannelMessage({tenantId:api.read().activeTenant,messageId:message.id,expectedText:message.text,action:"recall"});
 expect(api.read().messages.find(m=>m.id===message.id)).toMatchObject({recalled:true,attachments:message.attachments});atomic(()=>api.changeChannelMessage({tenantId:api.read().activeTenant,messageId:message.id,expectedText:message.text,action:"emoji",emoji:"👍"}));
});
