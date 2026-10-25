// Render the production policy form with explicit isolated API responses.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, RouterProvider, Outlet } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { AuthContext } from "../../src/components/auth"
import Channels from "../../src/app/intake-channels/page"
import { intakeChannelMessages } from "../../src/app/intake-channels/messages"
import "../../src/index.css"

const query = new URLSearchParams(location.search)
const locale = query.get("locale") ?? "en-US"
const labels = intakeChannelMessages[locale as keyof typeof intakeChannelMessages] ?? intakeChannelMessages["en-US"]
const row = {id:"80",kind:"API",version:3,availability:"AVAILABLE",credential_configured:true,settings:{callback_subscription_id:"9007199254740993",enabled:true,display_name:"Intake",support_email:"",language:locale,daily_limit:20,receipt_enabled:false,reminder_hours:48,timeout_hours:168}}
const writes: {body:unknown; key:string|null}[] = []
Object.assign(window, {callbackWrites:writes,callbackLabels:labels})
window.fetch = async (_input, init) => {
  if (init?.method === "PUT") {
    writes.push({body:JSON.parse(String(init.body)), key:new Headers(init.headers).get("Idempotency-Key")})
    if (query.has("lost") && writes.length === 1) throw new TypeError("Simulated lost response")
    return Response.json({code:0,data:{...row,version:4,settings:writes.at(-1)!.body}})
  }
  return Response.json({code:0,data:[row]})
}
const permissions = new Set(query.has("readonly") ? ["intake_read"] : ["intake_read","intake_channel_manage"])
const router = createMemoryRouter([{element:<AuthContext.Provider value={{session:{subject:"11",organization_id:"1"},status:"authenticated"} as never}><Outlet context={{permissions,permissionsResolved:true,tenantID:"1"}}/></AuthContext.Provider>,children:[{path:"/admin/:tenantID/entities/:entityID/intake-channels",element:<Channels/>}]}],{initialEntries:["/admin/1/entities/2/intake-channels"]})
void loadMessages(locale).then(messages => createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>))
