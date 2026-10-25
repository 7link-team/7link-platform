import { describe, expect, it } from "bun:test"
import { canEditArtifactComment } from "./artifact-comment-permissions"

describe("Artifact comment author editing window", () => {
  const created = Date.parse("2026-10-23T12:00:00Z")
  const message = { id: "1", thread_id: "2", seq: 1, body: "Original", created_by: "11", created_at: new Date(created).toISOString(), edit_count: 0 }
  it("requires the original author, an unretracted message, and a remaining edit", () => {
    expect(canEditArtifactComment(message, "11", created + 14 * 60000)).toBe(true)
    expect(canEditArtifactComment(message, "12", created)).toBe(false)
    expect(canEditArtifactComment({ ...message, edit_count: 10 }, "11", created)).toBe(false)
    expect(canEditArtifactComment({ ...message, retracted_at: new Date(created).toISOString(), body: null }, "11", created)).toBe(false)
  })
  it("expires after exactly 15 minutes and rejects invalid dates", () => {
    expect(canEditArtifactComment(message, "11", created + 15 * 60000)).toBe(true)
    expect(canEditArtifactComment(message, "11", created + 15 * 60000 + 1)).toBe(false)
    expect(canEditArtifactComment({ ...message, created_at: "unknown" }, "11", created)).toBe(false)
  })
})
