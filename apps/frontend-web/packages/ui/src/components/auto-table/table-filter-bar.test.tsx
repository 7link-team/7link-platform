import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlProvider } from "use-intl"
import { TableFilterBar } from "./table-filter-bar"
import en from "../../i18n/messages/en-US.json"
import zh from "../../i18n/messages/zh-CN.json"

describe("list filter presentation", () => {
  it("separates operators from exact ID and datetime values with readable labels", () => {
    for (const [locale, messages] of [
      ["en-US", en],
      ["zh-CN", zh],
    ] as const) {
      const html = renderToStaticMarkup(
        <IntlProvider locale={locale} messages={messages} timeZone="UTC">
          <TableFilterBar
            defaultValue={{
              owner: "eq:9007199254740993",
              created: "gte:2026-10-27T10:30:00Z",
              queue: "system:jobs",
            }}
            filters={[
              {
                field: "owner",
                name: "Owner",
                type: "string",
                conditions: ["eq"],
              },
              {
                field: "created",
                name: "Created",
                type: "datetime",
                conditions: ["gte", "lt"],
              },
              { field: "queue", name: "Queue", type: "string", conditions: [] },
            ]}
          />
        </IntlProvider>
      )
      expect(html).toContain('value="9007199254740993"')
      expect(html).toContain('value="system:jobs"')
      expect(html).toContain(messages.table.filter.op_gte)
      expect(html).toContain(
        'aria-label="Created ' + messages.table.filter.value + '"'
      )
      expect(html).not.toContain('value="eq:')
      expect(html).not.toContain('value="gte:')
      expect(html).not.toContain('type="datetime-local"')
    }
  })
})
