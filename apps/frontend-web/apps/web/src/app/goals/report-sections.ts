import type {ReportInput} from "./api"
const en={wentWell:"What went well (one item per line)",problems:"Problems (one item per line)",rootCauses:"Root causes (one item per line)",confirmAI:"I reviewed and accept this generated draft",expected:"Expected progress",actual:"Actual progress",source:"Fact source",completed:"Completed",uncompleted:"Uncompleted",retrospective:"Retrospective",next:"Next plan",fact:"Open actual work",unavailable:"Unavailable"}
const zh:typeof en={"wentWell": "做得好的事（每行一项）", "problems": "问题（每行一项）", "rootCauses": "根本原因（每行一项）", "confirmAI": "我已审阅并接受此生成草稿", "expected": "预期进度", "actual": "实际进度", "source": "事实来源", "completed": "已完成", "uncompleted": "未完成", "retrospective": "复盘", "next": "下期计划", "fact": "打开实际工作", "unavailable": "暂不可用"}
const ms:typeof en={wentWell:"Perkara yang baik (satu setiap baris)",problems:"Masalah (satu setiap baris)",rootCauses:"Punca utama (satu setiap baris)",confirmAI:"Saya menyemak dan menerima draf terjana ini",expected:"Kemajuan dijangka",actual:"Kemajuan sebenar",source:"Sumber fakta",completed:"Selesai",uncompleted:"Belum selesai",retrospective:"Retrospektif",next:"Pelan seterusnya",fact:"Buka kerja sebenar",unavailable:"Tidak tersedia"}
export function reportSectionLabels(locale:string){return locale.startsWith("zh")?zh:locale.startsWith("ms")?ms:en}
export function retrospectiveValues(values:Record<string,unknown>):Pick<ReportInput,"retrospective_sections"|"retrospective"|"retrospective_confirmed">{
 const lines=(key:string)=>String(values[key]??"").split(/\r?\n/).map(line=>line.trim()).filter(Boolean)
 const sections={went_well:lines("retro_went_well"),problems:lines("retro_problems"),root_causes:lines("retro_root_causes")}
 return {retrospective_sections:sections,retrospective:[...sections.went_well,...sections.problems,...sections.root_causes].join("\n"),retrospective_confirmed:Boolean(values.retrospective_confirmed)}
}
export function issueSection(code:string):string{return code.startsWith("B1")?"report-completed":code.startsWith("B3")||code.startsWith("N2")?"report-next-plan":code.startsWith("B4")||code.startsWith("B7")?"report-retrospective":"report-uncompleted"}
