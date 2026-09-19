import { describe, expect, it } from "bun:test"
import { normalizeOfficeEmbeddedImageURL } from "./office-preview-safety"

function octetDataURL(bytes: number[]) {
  return `data:application/octet-stream;base64,${btoa(
    String.fromCharCode(...bytes)
  )}`
}

describe("Office preview embedded image safety", () => {
  it.each([
    ["image/png", [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]],
    ["image/jpeg", [0xff, 0xd8, 0xff, 0xe0]],
    ["image/gif", [...Buffer.from("GIF89a")]],
    ["image/bmp", [...Buffer.from("BM")]],
    [
      "image/webp",
      [...Buffer.from("RIFF"), 0, 0, 0, 0, ...Buffer.from("WEBP")],
    ],
  ])(
    "normalizes a packaged %s resource after signature validation",
    (mime, bytes) => {
      const source = octetDataURL(bytes)
      expect(normalizeOfficeEmbeddedImageURL(source)).toBe(
        source.replace("application/octet-stream", mime as string)
      )
    }
  )

  it("rejects unknown, malformed and non-octet-stream data", () => {
    expect(
      normalizeOfficeEmbeddedImageURL(octetDataURL([...Buffer.from("<html")]))
    ).toBeUndefined()
    expect(
      normalizeOfficeEmbeddedImageURL(
        "data:application/octet-stream;base64,not-valid-***"
      )
    ).toBeUndefined()
    expect(
      normalizeOfficeEmbeddedImageURL("data:text/html;base64,PGh0bWw+")
    ).toBeUndefined()
    expect(
      normalizeOfficeEmbeddedImageURL("https://example.test/image.png")
    ).toBeUndefined()
  })
})
