import { test, expect } from "bun:test";
import { queryList, projectListExport } from "../src/list-query.js";
const columns=[{key:"name",label:"名称",value:r=>r.name,exportValue:r=>r.name},{key:"score",label:"分数",value:r=>r.score,exportValue:r=>r.score},{key:"version",label:"版本",value:r=>r.version,exportValue:r=>r.version}];
const ids=result=>result.rows.map(row=>row.id);
test("search and business filters query the authorized whole collection before pagination",()=>{
 const rows=Array.from({length:41},(_,i)=>({id:`R-${String(i).padStart(2,"0")}`,name:i===40?"Target keyword":"ordinary",score:i,version:1}));
 expect(ids(queryList(rows,{columns,query:" target KEYWORD ",page:9,pageSize:10}))).toEqual(["R-40"]);
 const result=queryList(rows,{columns,filterRow:r=>r.score%2===0,sort:"score",order:"desc",pageSize:10,page:2});
 expect(result.count).toBe(21);expect(result.authorizedCount).toBe(41);expect(result.offset).toBe(10);expect(ids(result)).toEqual(["R-20","R-18","R-16","R-14","R-12","R-10","R-08","R-06","R-04","R-02"]);
});
test("nullish empty invalid values stay last in both directions and equal values tie on stable ID",()=>{
 const rows=[{id:"z",score:2},{id:"b",score:null},{id:"a",score:2},{id:"e",score:""},{id:"c",score:NaN},{id:"d"},{id:"f",score:Infinity},{id:"low",score:1}];
 expect(ids(queryList(rows,{columns,sort:"score",order:"asc"}))).toEqual(["low","a","z","b","c","d","e","f"]);
 expect(ids(queryList(rows,{columns,sort:"score",order:"desc"}))).toEqual(["a","z","low","b","c","d","e","f"]);
 const values=[{id:"b",name:"item10"},{id:"a",name:"item2"}];expect(ids(queryList(values,{columns,sort:"name"}))).toEqual(["a","b"]);
});
test("sorting is immutable and every permutation preserves stable results",()=>{
 const rows=Array.from({length:40},(_,i)=>({id:`id-${String(i).padStart(2,"0")}`,score:i%4,name:`name${i%3}`})),before=structuredClone(rows);
 for(const sort of ["score","name"])for(const order of ["asc","desc"]){
  const wanted=queryList(rows,{columns,sort,order,pageSize:100}).rows.map(r=>r.id);
  for(let offset=0;offset<rows.length;offset++)expect(queryList([...rows.slice(offset),...rows.slice(0,offset)].reverse(),{columns,sort,order,pageSize:100}).rows.map(r=>r.id)).toEqual(wanted);
 }
 expect(rows).toEqual(before);
});
test("pagination clamps empty reduced and malformed pages and never returns a false empty page",()=>{
 const rows=Array.from({length:23},(_,i)=>({id:String(i),score:i}));
 for(const page of [0,-1,NaN,Infinity,"1e2","-2","wrong"]){const result=queryList(rows,{columns,page,pageSize:10});expect(result.page).toBe(1);expect(result.rows).toHaveLength(10);}
 const last=queryList(rows,{columns,page:99,pageSize:10,sort:"score"});expect(last.page).toBe(3);expect(last.rows).toHaveLength(3);expect(last.offset).toBe(20);
 const reduced=queryList(rows.slice(0,2),{columns,page:3,pageSize:10});expect(reduced.page).toBe(1);expect(reduced.rows).toHaveLength(2);
 const empty=queryList([],{columns,page:30,pageSize:10});expect(empty).toMatchObject({page:1,pageCount:1,count:0,offset:0,rows:[]});
 for(const pageSize of [0,-1,11,1e9,"bad",Infinity])expect(queryList(rows,{columns,pageSize}).pageSize).toBe(10);
});
test("all query pages partition exactly the filtered set without duplicate or missing records",()=>{
 const rows=Array.from({length:157},(_,i)=>({id:`R${i}`,score:i%13,name:i%2?"odd":"even"}));
 for(const pageSize of [10,20,50,100])for(const order of ["asc","desc"]){const first=queryList(rows,{columns,query:"even",sort:"score",order,pageSize});const all=[];for(let page=1;page<=first.pageCount;page++)all.push(...queryList(rows,{columns,query:"even",sort:"score",order,pageSize,page}).rows);expect(all).toEqual(first.matchedRows);expect(new Set(all.map(r=>r.id)).size).toBe(first.count);}
});
test("malformed record identities and business failures remain visible instead of returning an empty success",()=>{
 for(const rows of [[{name:"missing"}],[{id:""}],[{id:"dup"},{id:"dup"}],[{id:1},{id:"1"}]])expect(()=>queryList(rows,{columns})).toThrow();
 expect(()=>queryList([{id:"a"}],{columns,filterRow:()=>{throw Error("authorization unavailable");}})).toThrow("authorization unavailable");
 expect(()=>queryList([],{pageSizes:[]})).toThrow();
});
test("export projects only explicitly enabled columns for the complete matched query and records exact scope",()=>{
 const rows=Array.from({length:25},(_,i)=>({id:`ID${i}`,name:"visible",score:i,version:2,secret:"NEVER_EXPORT",privateHistory:["PRIVATE"]}));
 const query=queryList(rows,{columns,pageSize:10,page:2,filterRow:r=>r.score>2,sort:"score",order:"desc"});
 const payload=projectListExport(query.matchedRows,[...columns,{key:"history",value:r=>r.privateHistory}],{source:"authorized-resources",sort:query.sort,order:query.order});
 expect(payload.count).toBe(22);expect(payload.records).toHaveLength(22);expect(payload.records[0]).toEqual({id:"ID24",name:"visible",score:24,version:2});expect(payload.scope).toBe("当前查询结果");
 expect(JSON.stringify(payload)).not.toContain("NEVER_EXPORT");expect(JSON.stringify(payload)).not.toContain("PRIVATE");expect(JSON.stringify(payload)).not.toContain("privateHistory");
 expect(rows[0].secret).toBe("NEVER_EXPORT");
});
test("export does not invoke display renderers and rejects raw records cyclic or non-JSON fields",()=>{
 const row={id:"A",name:"<script>alert(1)</script>",secret:"no"};
 const safe=projectListExport([row],[{key:"name",render:()=>{throw Error("must not render");},exportValue:r=>r.name}],{source:"records"});expect(safe.records[0].name).toBe(row.name);expect(safe.records[0].version).toBeNull();
 expect(()=>projectListExport([row],[{key:"raw",exportValue:r=>r}],{source:"records"})).toThrow("原始记录");
 expect(()=>projectListExport([row],[{key:"nested",exportValue:r=>({nested:r})}],{source:"records"})).toThrow("原始记录");
 for(const value of [NaN,Infinity,1n,()=>{},new Map(),(()=>{const x={};x.self=x;return x;})()])expect(()=>projectListExport([row],[{key:"x",exportValue:()=>value}],{source:"records"})).toThrow();
 expect(()=>projectListExport([row],[{key:"name",value:r=>r.name}],{source:"records"})).toThrow("可导出字段");
 expect(()=>projectListExport([row],columns,{})).toThrow("来源");
});
