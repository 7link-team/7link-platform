globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const api=await import('../src/store');
const message=api.postChannelMessage({tenantId:api.read().activeTenant,channelId:'launch',text:'SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL'});
const draft=api.beginWorkDraft({sourceId:message.id,projectId:'launch'});
api.saveWorkDraft(draft,{title:'来源权限验证工作',criteria:'验证固定来源与撤回后的显示',project:'launch',type:'文档'},0);
const work=api.confirmWorkDraft(draft,1);
api.update(s=>{s.testConversationOrigin={workId:work.id,messageId:message.id};});
console.log(JSON.stringify(api.read()));
