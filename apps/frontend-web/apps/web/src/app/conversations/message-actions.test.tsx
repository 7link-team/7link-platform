import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createIamApi } from "../../lib/iam-api"
import { messageActionAvailability, createMessageActionsApi } from "./message-actions-api"
import { ConversationMarkdown } from "./message"

const now = Date.parse("2026-10-21T10:15:00Z")
const message = { id: "9007199254740993", author_id: "2", body: "original secret", version: 1, created_at: "2026-10-21T10:00:00Z" }
describe("message action boundaries", () => {
  it("allows only the author in the edit window and caps the tenth edit", () => {
    expect(messageActionAvailability(message,"2",false,now)).toEqual({ edit:true,retract:true })
    expect(messageActionAvailability(message,"4",false,now)).toEqual({ edit:false,retract:false })
    expect(messageActionAvailability(message,"4",true,now)).toEqual({ edit:false,retract:true })
    expect(messageActionAvailability({...message,version:11},"2",false,now).edit).toBe(false)
    expect(messageActionAvailability(message,"2",false,now+1).edit).toBe(false)
  })
  it("renders retracted nullable text without errors or original text", () => {
    expect(renderToStaticMarkup(<ConversationMarkdown body={null} />)).not.toContain("original secret")
    expect(messageActionAvailability({...message,retracted:true},"2",true,now)).toEqual({edit:false,retract:false})
  })
  it("limits author retraction to 24 hours and rejects invalid or future dates", () => {
    expect(messageActionAvailability(message,"2",false,Date.parse(message.created_at)+24*60*60_000).retract).toBe(true)
    expect(messageActionAvailability(message,"2",false,Date.parse(message.created_at)+24*60*60_000+1).retract).toBe(false)
    expect(messageActionAvailability({...message,created_at:"invalid"},"2",true,now)).toEqual({edit:false,retract:false})
    expect(messageActionAvailability(message,"2",false,Date.parse(message.created_at)-1)).toEqual({edit:false,retract:false})
  })
})

it("sends exact tenant/message IDs, If-Match and retry key and handles DELETE 204", async () => {
  const seen: {path:string;method:string;version:string|null;key:string|null;body:unknown}[]=[]
  const server=Bun.serve({port:0,async fetch(request){
    seen.push({path:new URL(request.url).pathname,method:request.method,version:request.headers.get("If-Match"),key:request.headers.get("Idempotency-Key"),body:request.method==="PATCH"?await request.json():null})
    if(request.method==="DELETE")return new Response(null,{status:204})
    return Response.json({code:0,data:request.method==="PATCH"?{...message,version:2}:[]})
  }})
  try {
    const api=createMessageActionsApi(createIamApi(`http://localhost:${server.port}`))
    await api.edit("7",message.id,1,"new text","logical-edit")
    await api.retract("7",message.id,2,"logical-retract")
    await api.revisions("7",message.id)
    expect(seen).toEqual([
      {path:`/tenants/7/messages/${message.id}`,method:"PATCH",version:'"1"',key:"logical-edit",body:{body:"new text"}},
      {path:`/tenants/7/messages/${message.id}`,method:"DELETE",version:'"2"',key:"logical-retract",body:null},
      {path:`/tenants/7/messages/${message.id}/revisions`,method:"GET",version:null,key:null,body:null},
    ])
  } finally {await server.stop(true)}
})
