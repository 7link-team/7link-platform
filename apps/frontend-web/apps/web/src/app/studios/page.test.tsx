import { expect, test } from "bun:test"
import { canConfirm, studioSource } from "./api"
import type { StudioRecord } from "./api"

test("assistant suggestions cannot be adopted without both accountable owner and valid deadline", () => {
 const draft = {kind: "ASSISTANT_DRAFT", state: "OPEN", content: {owner_id: "11", due_at: "2040-01-01T12:00:00Z"}} as StudioRecord
 expect(canConfirm(draft)).toBe(true)
 expect(canConfirm({...draft, content: {...draft.content, owner_id: undefined}})).toBe(false)
 expect(canConfirm({...draft, content: {...draft.content, due_at: "invalid"}})).toBe(false)
 expect(canConfirm({...draft, state: "CONFIRMED"})).toBe(false)
 expect(canConfirm({...draft, kind: "TASK"})).toBe(false)
})
test("source links never execute pasted javascript or local file URLs", () => {
 expect(studioSource("javascript:alert(1)")).toBeNull()
 expect(studioSource("file:///etc/passwd")).toBeNull()
 expect(studioSource("chapter:1#quote")).toBeNull()
 expect(studioSource("https://example.invalid/clip.mp4")).toBe("https://example.invalid/clip.mp4")
})
