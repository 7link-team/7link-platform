import { describe, expect, it } from "bun:test"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import { createIamApi } from "../../lib/iam-api"

describe("delivery view translations", () => {
  it("keeps board, dependency, defect and stage labels readable in every locale", () => {
    const keys = Object.keys(en).filter((key) =>
      /^(workView|workBoard|workDependenc|workSeverity|workFilter|workPriority|workReproducibility|workFoundVersion|workDefectStage|workState_|stage|assignment)/.test(
        key
      )
    )
    expect(keys.length).toBeGreaterThan(25)
    for (const messages of [en, zh, ms]) {
      for (const key of keys) {
        const value = (messages as Record<string, string>)[key]
        expect(value).toBeString()
        expect(value.trim().length).toBeGreaterThan(0)
        expect(value).not.toMatch(/\?{2,}|\uFFFD/)
        expect(value).not.toBe(key)
      }
    }
    for (const key of keys) {
      expect((zh as Record<string, string>)[key]).toMatch(/[\u3400-\u9FFF]/)
    }
  })
})

it("sends defect filters to the server with exact opaque accountable and submitter IDs", async () => {
  let seen: URL | undefined
  const server = Bun.serve({ port: 0, fetch(request) {
    seen = new URL(request.url)
    return Response.json({ code: 0, data: [], meta: { page: { total: 0 } } })
  } })
  try {
    const query = { offset: 0, limit: 20, severity: "eq:CRITICAL", accountable_principal_id: "eq:9007199254740993", created_by: "eq:9007199254740995" }
    await createIamApi(`http://localhost:${server.port}`).workItems("3", query)
    expect(seen?.pathname).toBe("/entities/3/work-items")
    expect(seen?.searchParams.get("severity")).toBe("eq:CRITICAL")
    expect(seen?.searchParams.get("accountable_principal_id")).toBe("eq:9007199254740993")
    expect(seen?.searchParams.get("created_by")).toBe("eq:9007199254740995")
    expect(seen?.searchParams.has("executor_principal_id")).toBe(false)
    expect(seen?.searchParams.has("handler")).toBe(false)
  } finally { await server.stop(true) }
})
