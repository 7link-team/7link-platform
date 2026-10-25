import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import {MachineFilters} from "./filters"

describe("machine list filters", () => {
  it("renders labeled localized filters with restored selections in all three languages", () => {
    for (const [locale, messages] of [["en-US", en], ["zh-CN", zh], ["ms-MY", ms]] as const) {
      const html = renderToStaticMarkup(<MemoryRouter initialEntries={["/?state=ONLINE&runtime=CODEX&free_slots=true"]}><IntlAppProvider initialLocale={locale} initialMessages={{iam: messages}}><MachineFilters/></IntlAppProvider></MemoryRouter>)
      expect(html).toContain(messages.fleetFreeSlotsOnly)
      expect(html).toContain(messages.fleetAllRuntimes)
      expect(html).toMatch(/value="ONLINE" selected=""/)
      expect(html).toMatch(/value="CODEX" selected=""/)
      expect(html).toContain('checked=""')
      expect(html.match(/<label/g)?.length).toBe(3)
    }
  })
})
