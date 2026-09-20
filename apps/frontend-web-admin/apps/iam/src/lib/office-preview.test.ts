import { describe, expect, it } from "bun:test"
import {
  officePreviewFit,
  presentationPageLabel,
} from "./office-preview"

describe("Office preview presentation", () => {
  it("uses readable page-width fitting for Word documents", () => {
    expect(officePreviewFit("report.docx", "")).toBe("width")
    expect(
      officePreviewFit(
        "report.bin",
        "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
      )
    ).toBe("width")
  })

  it("keeps spreadsheets at their native grid scale", () => {
    expect(officePreviewFit("budget.xlsx", "")).toBe("actual")
    expect(
      officePreviewFit(
        "budget.bin",
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
      )
    ).toBe("actual")
  })

  it("keeps presentations contained and labels their page navigation", () => {
    expect(officePreviewFit("deck.pptx", "")).toBe("contain")
    expect(presentationPageLabel(2, "Roadmap", "en-US")).toBe(
      "Slide 2: Roadmap"
    )
    expect(presentationPageLabel(2, "路线图", "zh-CN")).toBe(
      "第 2 页: 路线图"
    )
  })
})
