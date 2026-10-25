import { describe, expect, it } from "bun:test"
import { constitutionDiff } from "./constitution-diff"

describe("constitution comparison", () => {
  it("preserves context and positions across insertion and deletion", () => {
    expect(constitutionDiff("title\nend", "title\nadded\nend")).toEqual([
      { id: 0, kind: "unchanged", before: 1, after: 1, text: "title" },
      { id: 1, kind: "added", before: null, after: 2, text: "added" },
      { id: 2, kind: "unchanged", before: 2, after: 3, text: "end" },
    ])
    expect(
      constitutionDiff("title\nremoved\nend", "title\nend")[1]
    ).toMatchObject({
      kind: "removed",
      before: 2,
      after: null,
      text: "removed",
    })
  })

  it("losslessly reconstructs both versions including blank lines and Unicode", () => {
    const documents = [
      "",
      "a",
      "a\na",
      "a\n\nb",
      "# 规则\n\n保留证据",
      "a\nx\nc\ny\nz",
      "a\ny\nc\nx\nz",
      "\n",
      "a\n",
    ]
    for (const before of documents)
      for (const after of documents) {
        const rows = constitutionDiff(before, after)
        const oldRows = rows.filter((row) => row.kind !== "added")
        const newRows = rows.filter((row) => row.kind !== "removed")
        expect(oldRows.map((row) => row.text).join("\n")).toBe(before)
        expect(newRows.map((row) => row.text).join("\n")).toBe(after)
        expect(oldRows.map((row) => row.before)).toEqual(
          oldRows.map((_, i) => i + 1)
        )
        expect(newRows.map((row) => row.after)).toEqual(
          newRows.map((_, i) => i + 1)
        )
        expect(
          rows.filter((row) => row.kind !== "unchanged").length === 0
        ).toBe(before === after)
      }
  })

  it("compares documents at the byte limit without quadratic storage", () => {
    const before = `a${"\n".repeat(65534)}z`
    const after = `b${"\n".repeat(65534)}y`
    const rows = constitutionDiff(before, after)
    expect(rows.length).toBe(131070)
    expect(
      rows
        .filter((row) => row.kind !== "added")
        .map((row) => row.text)
        .join("\n")
    ).toBe(before)
    expect(
      rows
        .filter((row) => row.kind !== "removed")
        .map((row) => row.text)
        .join("\n")
    ).toBe(after)
  })
})
