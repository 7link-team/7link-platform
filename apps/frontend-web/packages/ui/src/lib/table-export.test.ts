import { describe, expect, it } from "bun:test"
import { tableCsv } from "./table-export"

describe("tableCsv", () => {
  it("preserves commas, quotes, newlines and non-ASCII cells", () => {
    expect(
      tableCsv([
        ["名称", 'a,"b"', "line\nnext"],
        ["", "ok", "42"],
      ])
    ).toBe('"名称","a,""b""","line\nnext"\r\n"","ok","42"')
  })

  it("neutralizes formula cells, including whitespace-prefixed formulas", () => {
    expect(tableCsv([["=SUM(A1)", " +1", "@cmd", "-2", "ordinary"]])).toBe(
      '"\'=SUM(A1)","\' +1","\'@cmd","\'-2","ordinary"'
    )
  })
})
