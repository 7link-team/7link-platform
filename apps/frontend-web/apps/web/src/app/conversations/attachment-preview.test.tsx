import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { AttachmentContent } from "./attachment-preview"
describe("pinned conversation media rendering", () => {
  it("uses native user-controlled audio and video without autoplay", () => {
    for (const mime of ["audio/webm", "video/mp4"]) {
      const html = renderToStaticMarkup(<AttachmentContent mime={mime} source="blob:test" text="" label="recording" />)
      expect(html).toContain("controls")
      expect(html).toContain('preload="none"')
      expect(html).not.toContain("autoplay")
      expect(html).toContain('aria-label="recording"')
    }
  })
  it("renders safe raster thumbnails but never executes SVG or HTML attachments", () => {
    const image = renderToStaticMarkup(<AttachmentContent mime="image/png" source="blob:test" text="" label="screenshot" />)
    expect(image).toContain('alt="screenshot"')
    expect(image).toContain("noopener noreferrer")
    expect(renderToStaticMarkup(<AttachmentContent mime="image/svg+xml" source="blob:test" text="" label="svg" />)).toBe("")
    const html = renderToStaticMarkup(<AttachmentContent mime="text/html" source="blob:test" text="<script>alert(1)</script>" label="code" />)
    expect(html).not.toContain("<script>")
    expect(html).toContain("&lt;script&gt;")
  })
  it("renders markdown as text and isolates PDF content", () => {
    const markdown = renderToStaticMarkup(<AttachmentContent mime="text/markdown" source="blob:test" text="**note** <img src=x>" label="note" />)
    expect(markdown).toContain("<strong>note</strong>")
    expect(markdown).not.toContain("<img")
    const pdf = renderToStaticMarkup(<AttachmentContent mime="application/pdf" source="blob:test" text="" label="document" />)
    expect(pdf).toContain('sandbox=""')
    expect(pdf).toContain('title="document"')
  })
})
