import { expect,it,spyOn } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter,Outlet,RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { PackageSend } from "./package-send"
import { iamApi } from "../../lib/iam-api"
import { clientworkApi,type ClientRecords } from "../../lib/clientwork-api"
const row:ClientRecords["packages"]={id:"9007199254740993",state:"DRAFT",version:1,archived_at:null,content_hash:"a".repeat(64),content:{title:"Release",release_notes:"Notes",artifacts:[]},items:[]}
it("publication and confirmation controls require the corresponding current internal authority in three locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const messages=await loadMessages(locale),labels=messages.iam as Record<string,string>
  expect(labels.packageSendNotice).not.toContain("???")
  for(const bundleReady of [true,false])for(const external of [true,false])for(const permissions of [[],["delivery_compose"],["delivery_send"],["delivery_compose","delivery_send"]]){
   const router=createMemoryRouter([{element:<Outlet context={{permissions:new Set(permissions),permissionsResolved:true}}/>,children:[{path:"/",element:<PackageSend project="3" row={row} external={external} bundleReady={bundleReady} mutate={async()=>{}}/>}]}])
   const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
   expect(html.includes(labels.packageRequestConfirmation!)).toBe(!external&&permissions.includes("delivery_compose"))
   expect(html.includes(labels.clientPublish!)).toBe(!external&&bundleReady&&permissions.includes("delivery_send"))
   expect(html.includes(labels.packageSendNotice!)).toBe(!external)
   router.dispose()
  }
 }
})
it("reuses canonical confirmation request/withdrawal routes with exact IDs and source version",async()=>{
 const calls:Parameters<typeof iamApi.deliveryResultRequest>[]=[]
 const request=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(...args)=>{calls.push(args);return {id:"9007199254740995"} as never})
 try{
  const result=await clientworkApi.requestDeliveryConfirmation("3",row.id!,7,"request-key")
  await clientworkApi.withdrawDeliveryConfirmation("3",result.id!,"withdraw-key")
  expect(calls[0]![0]).toEqual({method:"POST",url:`/entities/3/project/delivery-packages/${row.id}/confirmation-requests`})
  expect(calls[0]![1]?.body).toBe(JSON.stringify({source_version:7}));expect(calls[0]![1]?.headers).toEqual({"Idempotency-Key":"request-key"})
  expect(calls[1]![0]).toEqual({method:"PUT",url:"/entities/3/project/delivery-confirmations/9007199254740995/withdrawal"});expect(calls[1]![1]?.headers).toEqual({"Idempotency-Key":"withdraw-key"})
 }finally{request.mockRestore()}
})
