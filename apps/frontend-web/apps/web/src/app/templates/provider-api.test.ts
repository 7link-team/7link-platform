import {afterEach,expect,spyOn,test} from "bun:test"
import {iamApi} from "../../lib/iam-api"
import {providerApi,parseProviderConfig,providerKinds,type Provider,type ProviderAdapter} from "./provider-api"
let restore:(()=>void)|undefined
afterEach(()=>restore?.())
test("provider wire preserves snowflakes, nullable machine, licence, CAS and retry keys",async()=>{
 const calls:{route:{method:string;url:string};init?:RequestInit}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const signal=new AbortController().signal,body={kind:"IMAGE_GEN",adapter_key:"image_gen_http_v1",display_name:"Service",config:{endpoint:"https://example.invalid",model:"selected-model"},machine_id:null,accept_license_notice:true}
 await providerApi.create("7",body,"create",signal);await providerApi.create("7",body,"create",signal)
 expect(calls[0]).toEqual(calls[1]);expect(JSON.parse(calls[0]?.init?.body as string)).toEqual(body)
 await providerApi.create("7",{...body,machine_id:"9007199254740993"},"machine",signal)
 expect(JSON.parse(calls[2]?.init?.body as string).machine_id).toBe("9007199254740993")
 await providerApi.patch("7",{id:"9007199254740995",version:8} as Provider,{config:body.config,secret:"replacement",accept_license_notice:true},"patch",signal)
 expect(calls[3]?.init?.headers).toEqual({"Idempotency-Key":"patch","If-Match":"\"8\""})
 await providerApi.health("7","9007199254740995","health",signal)
 expect(calls[4]?.route.url).toBe("/tenants/7/template-providers/9007199254740995/health-checks")
 expect(calls[4]?.init?.headers).toEqual({"Idempotency-Key":"health"})
 await providerApi.bind("7","99",8,{IMAGE_GEN:"9007199254740995"},"bind",signal)
 expect(calls[5]?.init?.headers).toEqual({"Idempotency-Key":"bind","If-Match":"\"8\""})
 expect(calls.every(call=>call.init?.signal===signal)).toBe(true)
})
test("all generator families use declared public fields and keep credentials separate",()=>{
 expect(providerKinds).toHaveLength(7)
 for(const kind of providerKinds){const adapter={public_config_fields:["endpoint","model"]} as ProviderAdapter;expect(parseProviderConfig('{"endpoint":"https://example.invalid","model":"selected"}',adapter)).toEqual({endpoint:"https://example.invalid",model:"selected"});for(const raw of ['null','[]','{"credential":"private"}','{"endpoint":"x","api_key":"private"}'])expect(()=>parseProviderConfig(raw,adapter)).toThrow();expect(kind).toBeTruthy()}
})
