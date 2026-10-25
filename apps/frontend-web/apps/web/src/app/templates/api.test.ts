import { afterEach, expect, spyOn, test } from "bun:test"
import { iamApi } from "../../lib/iam-api"
import { templatesApi, rubricApi, rubricRoute, type RubricCommand } from "./api"
import { softwareDefinition } from "./workflow"
let restore: (() => void) | undefined
afterEach(() => {
  restore?.()
})

test("copy and deprecation retain exact source identity and retry keys",async()=>{
 const calls:{route:unknown;init:RequestInit|undefined}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never})
 restore=()=>spy.mockRestore()
 await templatesApi.copy("7","11","team-copy","Team copy","copy-retry")
 await templatesApi.copy("7","11","team-copy","Team copy","copy-retry")
 expect(calls[0]).toEqual(calls[1])
 expect(calls[0]?.route).toEqual({method:"POST",url:"/tenants/7/templates"})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({based_on_version_id:"11",key:"team-copy",name:"Team copy"})
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key":"copy-retry"})
 await templatesApi.deprecate("7","11","deprecate-retry")
 expect(calls[2]?.route).toEqual({method:"PUT",url:"/tenants/7/template-versions/11/deprecation"})
 expect(calls[2]?.init?.headers).toEqual({"Idempotency-Key":"deprecate-retry"})
})
test("verifier result reads retain exact work identity stage cursor and caller abort", async () => {
  const calls: { route: unknown; init: RequestInit | undefined }[] = []
  const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(
    async (route, init) => {
      calls.push({ route, init })
      return { items: [], has_more: false } as never
    }
  )
  restore = () => spy.mockRestore()
  const abort = new AbortController()
  await templatesApi.verifierResults(
    "7",
    "41",
    "AGENT_TEST",
    "123",
    abort.signal
  )
  expect(calls[0]?.route).toEqual({
    method: "GET",
    url: "/tenants/7/work-items/41/verifier-results?limit=50&stage=AGENT_TEST&cursor=123",
  })
  expect(calls[0]?.init?.signal).toBe(abort.signal)
  expect(calls[0]?.init?.body).toBeUndefined()
})
test("authenticated transport receives scoped routes, stable retry key and caller abort", async () => {
  const calls: { route: unknown; init: RequestInit | undefined }[] = []
  const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(
    async (route, init) => {
      calls.push({ route, init })
      return {} as never
    }
  )
  restore = () => spy.mockRestore()
  const definition = softwareDefinition()
  await templatesApi.draft("7", "software", definition, "retry-key")
  await templatesApi.draft("7", "software", definition, "retry-key")
  expect(calls[0]).toEqual(calls[1])
  expect(calls[0]?.route).toEqual({
    method: "POST",
    url: "/tenants/7/templates",
  })
  expect(calls[0]?.init?.headers).toEqual({ "Idempotency-Key": "retry-key" })
  expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({
    key: "software",
    definition,
  })
  const abort = new AbortController()
  await templatesApi.instance("7", "99", abort.signal)
  expect(calls.at(-1)?.init?.signal).toBe(abort.signal)
  await templatesApi.list("7", "cursor:a", abort.signal)
  expect(calls.at(-1)?.route).toEqual({
    method: "GET",
    url: "/tenants/7/templates?limit=30&sort=id&cursor=cursor%3Aa",
  })
  await templatesApi.instantiate("7", "8", "99", "11", "instance-key")
  expect(JSON.parse(calls.at(-1)?.init?.body as string)).toEqual({
    project_id: "99",
    accountable_principal_id: "11",
  })
})

test("library filters and family/version cursors reach authenticated server without narrowing in the browser",async()=>{
 const calls:{route:unknown;init:RequestInit|undefined}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {items:[],has_more:false} as never})
 restore=()=>spy.mockRestore()
 const signal=new AbortController().signal
 const filters={domain:"VIDEO",origin:"FIRST_PARTY",state:"PUBLISHED",team_mode:"HUMAN_ONLY"}
 await templatesApi.families("7","a & b",filters,signal)
 expect(calls[0]?.route).toEqual({method:"GET",url:"/tenants/7/template-families?limit=30&cursor=a+%26+b&domain=VIDEO&origin=FIRST_PARTY&state=PUBLISHED&team_mode=HUMAN_ONLY"})
 await templatesApi.list("7","123",signal,{...filters,key:"a & b"})
 expect(calls[1]?.route).toEqual({method:"GET",url:"/tenants/7/templates?limit=30&sort=id&cursor=123&domain=VIDEO&origin=FIRST_PARTY&state=PUBLISHED&team_mode=HUMAN_ONLY&key=a+%26+b"})
 expect(calls.every(call=>call.init?.signal===signal && call.init?.body===undefined)).toBe(true)
})

test("published rubric transport carries null private result, numeric scores, exact item scope and stable retry authority",async()=>{
 const calls:{route:unknown;init:RequestInit|undefined}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never})
 restore=()=>spy.mockRestore()
 const url=rubricRoute("7",undefined,"900","a".repeat(64)),signal=new AbortController().signal
 const body:RubricCommand={stage:"CLIENT_ACCEPTANCE",result_id:null,work_version:4,scores:[{dimension_key:"quality",score:4.5,note:"Actual published artifact",evidence_refs:[]}]}
 await rubricApi.record(url,body,"rubric-retry",signal);await rubricApi.record(url,body,"rubric-retry",signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method:"POST",url})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual(body)
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key":"rubric-retry"});expect(calls[0]?.init?.signal).toBe(signal)
})
