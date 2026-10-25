import { afterEach, describe, expect, it } from "bun:test"
import { isAcceptedResult, resultsApi, type WorkResult } from "./results-api"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"

const originalFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = originalFetch
})
const result: WorkResult = {
  id: "11",
  work_item_id: "7",
  project_id: "3",
  sequence: 1,
  summary: "Reviewed files",
  actor_id: "19",
  created_at: "2026-10-20T00:00:00Z",
  evidence: [],
  actions: [],
  handoffs: [],
}

describe("manual result contracts", () => {
  it("keeps historical-result feedback paginated and bound to its exact result ID", async () => {
    let requested = ""
    const feedback = {
      id: "91",
      result_version_id: "11",
      kind: "COMMENT" as const,
      detail: "Observed artifact",
      actor_id: "19",
      created_at: result.created_at,
    }
    globalThis.fetch = (async (url) => {
      requested = String(url)
      return Response.json({
        code: 0,
        data: [feedback],
        meta: { page: { total: 47 } },
      })
    }) as typeof fetch
    expect(await resultsApi.feedback("3", "7", "11", 20)).toEqual({
      items: [feedback],
      total: 47,
    })
    expect(requested).toEndWith(
      "/entities/3/work-items/7/results/11/feedback?offset=20&limit=20"
    )
  })

  it("posts categorized feedback with replay protection without asserting a work transition or external identity", async () => {
    let recorded: { url: string; init: RequestInit } | undefined
    const body = {
      kind: "FEEDBACK" as const,
      category: "SCOPE_CHANGE" as const,
      detail: "Please include the agreed migration notes.",
    }
    globalThis.fetch = (async (url, init) => {
      recorded = { url: String(url), init: init! }
      return Response.json({
        code: 0,
        data: {
          id: "91",
          result_version_id: "11",
          ...body,
          actor_id: "19",
          created_at: result.created_at,
        },
      })
    }) as typeof fetch
    const response = await resultsApi.postFeedback(
      "3",
      "7",
      "11",
      "feedback-replay",
      body
    )
    expect(response.result_version_id).toBe("11")
    expect(recorded!.url).toEndWith(
      "/entities/3/work-items/7/results/11/feedback"
    )
    expect(recorded!.init.method).toBe("POST")
    expect(new Headers(recorded!.init.headers).get("Idempotency-Key")).toBe(
      "feedback-replay"
    )
    expect(new Headers(recorded!.init.headers).has("If-Match")).toBe(false)
    expect(JSON.parse(String(recorded!.init.body))).toEqual(body)
  })

  it("surfaces denied feedback rather than showing a posted record", async () => {
    globalThis.fetch = (async () =>
      Response.json(
        { code: 100403, message: "Forbidden" },
        { status: 403 }
      )) as unknown as typeof fetch
    await expect(
      resultsApi.postFeedback("3", "7", "11", "feedback-replay", {
        kind: "COMMENT",
        detail: "Note",
      })
    ).rejects.toMatchObject({ status: 403 })
  })
  it("resolves real immutable file metadata through the work-authorized candidate endpoint", async () => {
    let requested = ""
    const candidate = {
      space_id: "41",
      file_id: "42",
      version_id: "43",
      file_version: 6,
      name: "evidence.txt",
      sha256: "a".repeat(64),
      mime: "text/plain",
      size: 5,
    }
    globalThis.fetch = (async (url) => {
      requested = String(url)
      return Response.json({ code: 0, data: candidate })
    }) as typeof fetch
    expect(await resultsApi.candidate("3", "7", "42")).toEqual(candidate)
    expect(requested).toEndWith(
      "/entities/3/work-items/7/evidence-candidates/42"
    )
  })
  it("scopes lists and preserves the server total for version pagination", async () => {
    let requested = ""
    globalThis.fetch = (async (input) => {
      requested = String(input)
      return Response.json({
        code: 0,
        data: [result],
        meta: { page: { total: 101 } },
      })
    }) as typeof fetch
    expect(await resultsApi.list("3", "7", 50)).toEqual({
      items: [result],
      total: 101,
    })
    expect(requested).toEndWith(
      "/entities/3/work-items/7/results?offset=50&limit=50"
    )
  })

  it("preserves exact file pins, quoted work version, replay key and reasoned return body", async () => {
    const recorded: { url: string; init: RequestInit }[] = []
    globalThis.fetch = (async (url, init) => {
      recorded.push({ url: String(url), init: init! })
      return Response.json({ code: 0, data: result })
    }) as typeof fetch
    const evidence = {
      space_id: "41",
      file_id: "42",
      file_version: 6,
      version_id: "43",
      criterion_id: "44",
      coverage: "COVERED",
    }
    await resultsApi.mutate("3", "7", "", 9, "same-retry-key", {
      summary: "Fixed result",
      evidence: [evidence],
    })
    await resultsApi.mutate(
      "3",
      "7",
      "/11/handoffs/51/receipt",
      10,
      "return-key",
      { accepted: false, reason: "Missing agreed artifact" }
    )
    expect(recorded[0]!.url).toEndWith("/entities/3/work-items/7/results")
    const headers = new Headers(recorded[0]!.init.headers)
    expect(headers.get("If-Match")).toBe('"9"')
    expect(headers.get("Idempotency-Key")).toBe("same-retry-key")
    expect(headers.get("Content-Type")).toBe("application/json")
    expect(recorded[0]!.init.method).toBe("POST")
    expect(JSON.parse(String(recorded[0]!.init.body))).toEqual({
      summary: "Fixed result",
      evidence: [evidence],
    })
    expect(recorded[1]!.url).toEndWith("/results/11/handoffs/51/receipt")
    expect(JSON.parse(String(recorded[1]!.init.body))).toEqual({
      accepted: false,
      reason: "Missing agreed artifact",
    })
  })

  it("surfaces stale version failures without claiming a recorded result", async () => {
    globalThis.fetch = (async () =>
      Response.json(
        { code: 100409, message: "Version changed" },
        { status: 412 }
      )) as unknown as typeof fetch
    await expect(
      resultsApi.mutate("3", "7", "/11/acceptance", 2, "retry", {
        reason: "Reviewed",
      })
    ).rejects.toMatchObject({ status: 412 })
  })

  it("downloads the authorized pinned evidence bytes rather than current file content", async () => {
    let requested = ""
    const bytes = new Uint8Array([0, 255, 13, 10, 128])
    globalThis.fetch = (async (url) => {
      requested = String(url)
      return new Response(bytes)
    }) as typeof fetch
    const blob = await resultsApi.evidence("3", "7", "11", "81")
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytes)
    expect(requested).toEndWith(
      "/entities/3/work-items/7/results/11/evidence/81/content"
    )
  })

  it("never carries an old acceptance onto a new result version", () => {
    expect(
      isAcceptedResult(result, {
        current_claim_id: "11",
        accepted_at: "2026-10-20T00:00:00Z",
      })
    ).toBe(true)
    expect(
      isAcceptedResult(result, {
        current_claim_id: "12",
        accepted_at: "2026-10-20T00:00:00Z",
      })
    ).toBe(false)
    expect(
      isAcceptedResult(
        { ...result, id: "12" },
        { current_claim_id: "12", accepted_at: null }
      )
    ).toBe(false)
    expect(isAcceptedResult(result, {})).toBe(false)
  })

  it("provides real result workflow translations in all three locales", () => {
    const keys = Object.keys(en).filter((key) => key.startsWith("result"))
    expect(keys.length).toBeGreaterThan(40)
    for (const key of keys) {
      for (const messages of [en, zh, ms]) {
        const text = (messages as Record<string, string>)[key]
        expect(text).toBeString()
        expect(text?.trim()).not.toBe("")
        expect(text).not.toMatch(/\?{2,}|\uFFFD/)
      }
      expect((zh as Record<string, string>)[key]).toMatch(/[\u3400-\u9FFF]/)
      expect((ms as Record<string, string>)[key]).not.toBe(
        (en as Record<string, string>)[key]
      )
    }
  })
})
