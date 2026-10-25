import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { ChannelSearchCard } from "./media-tools"
describe("actual provider search card", () => {
  it("preserves citations and escapes provider text", () => {
    const html = renderToStaticMarkup(<ChannelSearchCard card={{ query: "<query>", sources: [{ title: "<source>", url: "https://example.com/standard", summary: "<script>unsafe</script>" }], conclusions: [{ text: "cited fact", sources: [1] }] }} />)
    expect(html).toContain("&lt;query&gt;")
    expect(html).toContain("&lt;source&gt;")
    expect(html).not.toContain("<script>")
    expect(html).toContain("[1]")
    expect(html).toContain("noopener noreferrer")
  })
})
