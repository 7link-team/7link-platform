import { afterEach, describe, expect, it } from "bun:test"
import { scopeBody, submitScopeChange, type ScopeValues } from "./changes-api"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"

const original = globalThis.fetch
afterEach(() => {
  globalThis.fetch = original
})
const values: ScopeValues = {
  title: "Revised scope",
  description: "Agreed work",
  priority: 2,
  summary: "Approved change rationale",
  action: "NONE",
  criterion_id: "9007199254740993",
  text: "Check this outcome",
  required: true,
}

describe("scope baseline command", () => {
  it("preserves the exact source version and explicit criterion operation without inventing work mappings", () => {
    const detail = { current_version_no: 3 }
    expect(scopeBody(detail, values)).toEqual({
      base_version_no: 3,
      title: values.title,
      description: values.description,
      priority: 2,
      summary: values.summary,
      criteria: {},
    })
    expect(scopeBody(detail, { ...values, action: "ADD" }).criteria).toEqual({
      add: [{ text: values.text, required: true }],
    })
    expect(scopeBody(detail, { ...values, action: "RETIRE" }).criteria).toEqual(
      { retire: [values.criterion_id] }
    )
    expect(
      scopeBody(detail, { ...values, action: "SUPERSEDE", required: false })
        .criteria
    ).toEqual({
      supersede: [
        {
          criterion_id: values.criterion_id,
          text: values.text,
          required: false,
        },
      ],
    })
  })
  it("sends current If-Match, one replay identity and the actual target-bound proof unchanged", async () => {
    const calls: { url: string; init: RequestInit }[] = []
    globalThis.fetch = (async (url, init) => {
      calls.push({ url: String(url), init: init! })
      return Response.json({ code: 0, data: { id: "41" } })
    }) as typeof fetch
    const body = {
      ...scopeBody({ current_version_no: 3 }, values),
      reauth_proof: "actual-proof",
    }
    const path = "/entities/7/requirements/9/change-records"
    await submitScopeChange(path, 12, "same-attempt", body)
    await submitScopeChange(path, 12, "same-attempt", body)
    expect(calls).toHaveLength(2)
    for (const call of calls) {
      expect(call.url).toEndWith(path)
      const headers = new Headers(call.init.headers)
      expect(headers.get("If-Match")).toBe('"12"')
      expect(headers.get("Idempotency-Key")).toBe("same-attempt")
      expect(JSON.parse(String(call.init.body))).toEqual(body)
    }
  })
  it("provides all scope action labels and the accepted-history warning in every locale", () => {
    for (const messages of [en, zh, ms]) {
      for (const key of Object.keys(en).filter((k) => k.startsWith("scope"))) {
        const value = (messages as Record<string, string>)[key]
        expect(value).toBeString()
        expect(value.trim().length).toBeGreaterThan(0)
        expect(value).not.toMatch(/\?{3}|\ufffd/)
      }
    }
  })
})
