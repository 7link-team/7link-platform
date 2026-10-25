import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { ConversationMarkdown } from "./message"

describe("conversation Markdown safety", () => {
  it("renders author text without executing raw HTML or script links", () => {
    const html = renderToStaticMarkup(
      <ConversationMarkdown
        body={
          "<img src=x onerror=alert(1)> [click](javascript:alert(1)) **safe**"
        }
      />
    )
    expect(html).not.toContain("<img")
    expect(html).not.toContain('href="javascript:')
    expect(html).toContain("&lt;img")
    expect(html).toContain("safe")
  })
  it("opens permitted links with opener protection", () => {
    const html = renderToStaticMarkup(
      <ConversationMarkdown body="[Guide](https://example.invalid/guide)" />
    )
    expect(html).toContain('href="https://example.invalid/guide"')
    expect(html).toContain("noopener")
  })
})
