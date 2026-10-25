import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {WorkdirList} from "./workdirs"

test("workdir lifecycle preserves archive facts and hides writes for read-only viewers in all locales", async () => {
  const rows = [{id: "9007199254740993", project_id: "21", repository_id: "31", path: "D:/<private>", version: 4, archived_at: "2026-10-22T12:00:00Z"}]
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><WorkdirList machineID="11" rows={rows} manage={false} probe={false} online={false} reload={async () => {}}/></IntlAppProvider>)
    expect(html).toContain("D:/&lt;private&gt;"); expect(html).toContain(rows[0]!.archived_at)
    expect(html).not.toContain("<button"); expect(html).not.toContain("<form"); expect(html).not.toContain("workdirLifecycleHint"); expect(html).not.toContain("????")
  }
})
