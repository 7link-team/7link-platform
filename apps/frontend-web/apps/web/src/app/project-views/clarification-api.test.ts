import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createClarificationApi, type Clarification } from "./clarification-api"

it("preserves opaque source/recipient IDs and posts versioned per-question answers", async () => {
  const seen: {path: string; body: unknown; version: string | null; key: string | null}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    seen.push({path: new URL(request.url).pathname, body: request.method === "POST" ? await request.json() : null, version: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key")})
    return Response.json({code: 0, data: {id: "9007199254740993", version: 2, state: "OPEN"}})
  }})
  try {
    const api = createClarificationApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const body = {intake_id: "9007199254740995", intake_version: 4, recipient_id: "9007199254740997", questions: ["Which format?"]}
    await api.request("3", body, "request-key")
    expect(seen[0]).toEqual({path: "/entities/3/client-clarifications", body, version: null, key: "request-key"})
    const round: Clarification = {id: "9007199254740993", intake_id: body.intake_id, recipient_id: body.recipient_id, state: "OPEN", version: 2, questions: [], reminders_sent: 0, timing_available: false, available_via: "IN_APP", created_at: "2026-10-21T00:00:00Z"}
    await api.answer("3", round, {question_no: 2, answer: "CSV <script>"}, "answer-key")
    expect(seen[1]).toEqual({path: "/entities/3/client-clarifications/9007199254740993/answers", body: {question_no: 2, answer: "CSV <script>"}, version: '"2"', key: "answer-key"})
    await api.cancel("3", round, "Question no longer needed", "cancel-key")
    expect(seen[2]).toEqual({path: "/entities/3/client-clarifications/9007199254740993/cancellation", body: {reason: "Question no longer needed"}, version: '"2"', key: "cancel-key"})
    for (const patch of [{state: "CANCELLED"}, {version: 0}, {version: 1.5}, {id: null}]) expect(() => api.cancel("3", {...round, ...patch}, "Reason", "key")).toThrow()
    for (const reason of [" ", "x".repeat(2001)]) expect(() => api.cancel("3", round, reason, "key")).toThrow()
    expect(seen).toHaveLength(3)
    for (const patch of [{state: "ANSWERED"}, {version: 0}, {version: 1.5}, {id: null}]) expect(() => api.answer("3", {...round, ...patch}, {question_no: 1, answer: "UTC"}, "key")).toThrow()
    for (const value of ["0", "../4", "1e3"]) expect(() => api.request(value, body, "key")).toThrow()
  } finally { await server.stop(true) }
})
