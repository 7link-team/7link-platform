import { expect,it } from "bun:test"
import { packageSourceRows } from "../../lib/clientwork-api"

it("preserves source table row positions including quoted delimiters, newlines and empty fields",()=>{
 expect(packageSourceRows('name,value\n"first, item","line one\nline two"\n,\nlast,"quote ""inside"""\n',"source.csv")).toEqual([["first, item","line one\nline two"],["",""],["last",'quote "inside"']])
 expect(packageSourceRows('name\n""\nvalue\n',"source.csv")).toEqual([[""],["value"]])
 expect(packageSourceRows('name\tvalue\nfirst\tsecond',"source.tsv")).toEqual([["first","second"]])
 expect(packageSourceRows('[{"result":"failed"},[1,2]]',"source.json")).toEqual(['{"result":"failed"}','[1,2]'])
 expect(packageSourceRows('[{"count":9007199254740993,"text":", [ literal"},[1,2]]',"source.json")).toEqual(['{"count":9007199254740993,"text":", [ literal"}','[1,2]'])
 for(const [text,name] of [['a,b\nwrong',"source.csv"],['a\n"unfinished',"source.csv"],['{}',"source.json"],['a\nb',"source.pdf"]])expect(()=>packageSourceRows(text!,name!)).toThrow()
})
