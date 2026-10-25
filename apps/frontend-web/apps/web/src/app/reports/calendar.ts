export function calendarPeriods(kind:string,now=new Date(),timezone?:string):string[]{
 const parts=timezone?new Intl.DateTimeFormat("en-CA",{timeZone:timezone,year:"numeric",month:"numeric",day:"numeric"}).formatToParts(now):null
 const part=(key:string)=>Number(parts?.find(p=>p.type===key)?.value)
 const local=parts?new Date(part("year"),part("month")-1,part("day"),12):new Date(now.getFullYear(),now.getMonth(),now.getDate(),12)
 if(kind==="DAY")local.setDate(local.getDate()-(local.getDay()+6)%7)
 if(kind==="WEEK"){local.setDate(local.getDate()-(local.getDay()+6)%7-49)}
 if(kind==="MONTH"){local.setDate(1);local.setMonth(local.getMonth()-11)}
 return Array.from({length:kind==="DAY"?7:kind==="WEEK"?8:12},(_,i)=>{const d=new Date(local);if(kind==="MONTH")d.setMonth(d.getMonth()+i);else d.setDate(d.getDate()+i*(kind==="WEEK"?7:1));return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`})
}
export function calendarEnd(kind:string,last:string):string {
 const date=new Date(`${last}T12:00:00Z`)
 if(kind==="MONTH")date.setUTCMonth(date.getUTCMonth()+1)
 else date.setUTCDate(date.getUTCDate()+(kind==="WEEK"?7:1))
 return date.toISOString().slice(0,10)
}
