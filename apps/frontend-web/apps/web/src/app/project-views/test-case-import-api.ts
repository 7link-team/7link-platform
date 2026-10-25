import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"

type ImportCase={title:string;criterion_source?:string;preconditions?:string;steps:string;expected:string;priority?:string;criterion_id?:string;requirement_id?:string}
export type TestCaseImportReport={imported:number;unclassified:number;preview:boolean;columns:string[];rows:{row:number;source:string[];case?:ImportCase;unclassified:boolean;case_id?:string;version?:number}[];errors:{row:number;column:string;code:string}[]}
export function importTestCaseCSV(project:string,work:string,requirement:string,file:File,preview:boolean,key:string){
 const body=new FormData();body.set("file",file)
 const query=new URLSearchParams({work_item_id:collaborationID(work),requirement_id:collaborationID(requirement),preview:String(preview)})
 return iamApi.deliveryResultRequest<TestCaseImportReport>({method:"POST",url:`/entities/${collaborationID(project)}/test-case-imports?${query}`},{headers:{"Idempotency-Key":key},body})
}
export const testCaseCSVColumns=["title","criterion","preconditions","steps","expected","priority"]
function quoted(value:string){const safe=/^[\s]*[=+@-]/.test(value)?`'${value}`:value;return `"${safe.replaceAll('"','""')}"`}
export function testCaseFailureCSV(report:TestCaseImportReport){
 const rows=[report.columns.concat(["row","column","error"])]
 for(const issue of report.errors){const source=report.rows.find(row=>row.row===issue.row)?.source??Array.from({length:report.columns.length},()=>"");rows.push([...source,String(issue.row),issue.column,issue.code])}
 return "\ufeff"+rows.map(row=>row.map(quoted).join(",")).join("\r\n")
}
