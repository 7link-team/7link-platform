import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ReviewAttention} from "./attention"
test("attention initial status never claims time before a real heartbeat in each locale",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=await loadMessages(locale);const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ReviewAttention project="99" work="7" claim="8"/></IntlAppProvider>);expect(html).toContain('role="status"');expect(html).not.toContain("attentionWaiting");expect(html).not.toContain("15000")}
})
