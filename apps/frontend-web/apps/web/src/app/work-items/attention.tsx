import {useEffect,useState} from "react"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {attentionApi,AttentionClock,type AttentionSession} from "./attention-api"

export function ReviewAttention({project,work,claim,surface="REVIEW_DESK",interaction}:{project:string;work:string;claim:string;surface?:"REVIEW_DESK"|"INBOX";interaction?:string}){
 const t=useTranslations("iam"),[state,setState]=useState<"waiting"|"active"|"unavailable">("waiting")
 useEffect(()=>{
  let stopped=false,flight=false,session:AttentionSession|null=null,sequence=1,pending:{sequence:number;delta:number;ended:boolean}|null=null
  const clock=new AttentionClock(performance.now(),document.visibilityState==="visible",document.hasFocus())
  const presence=()=>clock.presence(performance.now(),document.visibilityState==="visible",document.hasFocus())
  const activity=()=>clock.activity(performance.now())
  const events=["pointerdown","pointermove","keydown","wheel","scroll","touchstart"]
  for(const event of events)window.addEventListener(event,activity,{passive:true})
  window.addEventListener("focus",presence);window.addEventListener("blur",presence);document.addEventListener("visibilitychange",presence)
  let key=crypto.randomUUID()
  const flush=async(ended=false)=>{
   if(flight)return
   flight=true
   try{
    if(!session){session=await attentionApi.start(project,work,claim,key,surface,interaction);sequence=session.last_sequence+1}
    if(!pending)pending={sequence,delta:clock.take(performance.now()),ended:ended||stopped}
    const result=await attentionApi.beat(project,work,session.id,pending.sequence,pending.delta,pending.ended)
    sequence=result.last_sequence+1;pending=null
    if(!stopped)setState("active")
    if(stopped&&!result.ended_at){await attentionApi.beat(project,work,session.id,sequence,clock.take(performance.now()),true)}
   }catch(error){
    if(!stopped)setState("unavailable")
    // A closed session needs a new request identity; transient failures keep the exact sequence/delta.
    if(error&&typeof error==="object"&&"status" in error&&error.status===409){session=null;pending=null;key=crypto.randomUUID();clock.take(performance.now())}
   }finally{flight=false}
  }
  // Opening a view is not activity. The first measured interval follows an actual input event.
  void flush()
  const timer=window.setInterval(()=>{void flush()},15000)
  return()=>{stopped=true;window.clearInterval(timer);for(const event of events)window.removeEventListener(event,activity);window.removeEventListener("focus",presence);window.removeEventListener("blur",presence);document.removeEventListener("visibilitychange",presence);void flush(true)}
 },[project,work,claim,surface,interaction])
 return <p className="text-xs text-muted-foreground" role="status">{t(state==="unavailable"?"attentionUnavailable":state==="active"?"attentionActive":"attentionWaiting")}</p>
}
