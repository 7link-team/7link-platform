import { describe, expect, it } from "bun:test"
import { foldSystemMessages, type SystemMessage } from "./system-message-folding"
describe("consecutive system event folding", () => {
  const event = (body: string, extra: Partial<SystemMessage> = {}): SystemMessage => ({ kind: "EVENT", fold_key: "execution:1", body, ...extra })
  it("caps groups at fifty and preserves chronological expansion", () => {
    const groups = foldSystemMessages(Array.from({ length: 51 }, (_, index) => event(String(index))))
    expect(groups).toHaveLength(2)
    expect(groups[0].latest.body).toBe("49")
    expect(groups[0].previous).toHaveLength(49)
    expect(groups[0].previous[0].body).toBe("0")
    expect(groups[1].latest.body).toBe("50")
  })
  it("does not cross text, differing keys, attention or pending card barriers", () => {
    for (const barrier of [event("attention", { attention: true }), event("decision", { kind: "DECISION_CARD" }), event("proposal", { kind: "DISPATCH_CARD", payload: { status: "PROPOSED" } }), event("key", { fold_key: "other" }), event("text", { kind: "TEXT" })]) expect(foldSystemMessages([event("before"), barrier, event("after")])).toHaveLength(3)
  })
  it("folds resolved dispatch cards with matching events", () => {
    const resolved = event("resolved", { kind: "DISPATCH_CARD", payload: { status: "APPLIED" } })
    expect(foldSystemMessages([resolved, { ...resolved, body: "later" }])).toHaveLength(1)
    expect(foldSystemMessages([event("before"), resolved])).toHaveLength(1)
  })
})
