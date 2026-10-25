import type {GoalInput, GoalTreeRow} from "./api"
const text = {
 "en-US": {tree:"Goal tree", list:"List", all:"All", origin:"Member type", human:"Human", manual:"Human-created Agent", spawned:"Agent-created Agent", elapsed:"Time elapsed (workdays)", progress:"KR progress", unknown:"Unavailable", hint:"Filters retain authorized ancestor paths. Hidden parent details are omitted."},
 "zh-CN": {"tree": "目标树", "list": "列表", "all": "全部", "origin": "成员类型", "human": "人类", "manual": "人类创建的 Agent", "spawned": "Agent 创建的 Agent", "elapsed": "已过时间（工作日）", "progress": "KR 进度", "unknown": "暂不可用", "hint": "筛选保留有权查看的父级路径，隐藏父级的详情不显示。"},
 "ms-MY": {tree:"Pokok matlamat",list:"Senarai",all:"Semua",origin:"Jenis ahli",human:"Manusia",manual:"Ejen ciptaan manusia",spawned:"Ejen ciptaan ejen",elapsed:"Masa berlalu (hari bekerja)",progress:"Kemajuan KR",unknown:"Tidak tersedia",hint:"Penapis mengekalkan laluan induk yang dibenarkan. Butiran induk tersembunyi tidak dipaparkan."},
}
export type GoalFilters = {project:string; owner:string; state:string; period:string; start:string; origin:string}
export function goalTreeVisible(rows: GoalTreeRow[], filters: GoalFilters): Set<string> {
 const byID=new Map(rows.map(row=>[row.id,row]))
 const visible=new Set<string>()
 for(const row of rows) {
  if((filters.project && row.project_id!==filters.project)||(filters.owner && row.principal_id!==filters.owner)||(filters.state && row.state!==filters.state)||(filters.period && row.period_kind!==filters.period)||(filters.start && row.period_start!==filters.start)||(filters.origin && row.origin!==filters.origin)) continue
  let current:GoalTreeRow|undefined=row
  const path=new Set<string>()
  while(current && !path.has(current.id)) {path.add(current.id);visible.add(current.id);current=byID.get((current.content as GoalInput).parent_goal_id??"")}
 }
 return visible
}
export function krProgress(kr: GoalInput["key_results"][number]): number {
 const span=kr.target-kr.baseline
 return span===0 ? (kr.current===kr.target?1:0) : Math.max(0,Math.min(1,(kr.current-kr.baseline)/span))
}
export function goalViewLabels(locale:string){return text[locale as keyof typeof text]??text["en-US"]}
