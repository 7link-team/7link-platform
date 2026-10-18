import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlProvider } from "use-intl"
import { AutoTable } from "./auto-table"
import messages from "../../i18n/messages/en-US.json"

describe("AutoTable externally managed rows", () => {
  it("preserves row metadata and omits duplicate pagination", () => {
    const html = renderToStaticMarkup(
      <IntlProvider locale="en-US" timeZone="UTC" messages={messages}>
        <AutoTable
          data={[{ id: "record", name: "Existing" }]}
          rowKey="id"
          pagination={false}
          rowProps={(row) => ({
            "data-record-id": row.id,
            "data-state": "selected",
          })}
          columns={[{ field: "name", name: "Name" }]}
        />
      </IntlProvider>
    )
    expect(html).toContain('data-record-id="record"')
    expect(html).toContain('data-state="selected"')
    expect(html).toContain("Existing")
    expect(html).not.toContain("<select")
    expect(html).not.toContain('aria-label="Next page"')
  })

  it("lets a page own its empty state without another empty row", () => {
    const html = renderToStaticMarkup(
      <IntlProvider locale="en-US" timeZone="UTC" messages={messages}>
        <AutoTable
          data={[]}
          emptyContent={null}
          pagination={false}
          columns={[{ field: "id", name: "ID" }]}
        />
      </IntlProvider>
    )
    expect(html).not.toContain("<td")
  })
})
