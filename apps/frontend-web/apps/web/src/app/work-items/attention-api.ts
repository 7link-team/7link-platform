import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"
export type AttentionSession={id:string;active_ms:number;last_sequence:number;ended_at:string|null;heartbeat_interval_ms:number;idle_cutoff_ms:number;reviewer_lease_expires_at:string|null}
export const createAttentionApi=(api:Pick<typeof iamApi,"deliveryResultRequest">)=>{
 const path=(project:string,work:string)=>`/entities/${collaborationID(project)}/work-items/${collaborationID(work)}/attention-sessions`
 return {
  start:(project:string,work:string,claim:string,key:string,surface:"REVIEW_DESK"|"INBOX"="REVIEW_DESK",interaction?:string)=>api.deliveryResultRequest<AttentionSession>({method:"POST",url:path(project,work)},{headers:{"Idempotency-Key":key},body:JSON.stringify({... (interaction ? {interaction_id:collaborationID(interaction)} : {claim_id:collaborationID(claim)}),surface})}),
  beat:(project:string,work:string,id:string,sequence:number,delta:number,ended:boolean)=>{
   if(!Number.isSafeInteger(sequence)||sequence<1||!Number.isSafeInteger(delta)||delta<0)throw new Error("Invalid attention heartbeat")
   return api.deliveryResultRequest<AttentionSession>({method:"PUT",url:`${path(project,work)}/${collaborationID(id)}`},{body:JSON.stringify({sequence,active_ms_delta:delta,ended}),keepalive:ended})
  },
 }
}
export const attentionApi=createAttentionApi(iamApi)

// Integrate only visible, focused intervals intersecting the recent-activity window.
export class AttentionClock {
 private cursor:number
 private lastActivity:number=Number.NEGATIVE_INFINITY
 private visible:boolean
 private focused:boolean
 private pending=0
 constructor(now:number,visible:boolean,focused:boolean){this.cursor=now;this.visible=visible;this.focused=focused}
 private advance(now:number){
  if(this.visible&&this.focused)this.pending+=Math.max(0,Math.min(now,this.lastActivity+60000)-this.cursor)
  this.cursor=now
 }
 activity(now:number){this.advance(now);this.lastActivity=now}
 presence(now:number,visible:boolean,focused:boolean){this.advance(now);this.visible=visible;this.focused=focused}
 take(now:number){this.advance(now);const value=Math.floor(this.pending);this.pending-=value;return value}
}
