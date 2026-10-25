import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { compileRequestContract } from "@workspace/ui/components/auto-form/contract"
import { buildAgentCreation } from "./registration-api"

describe("atomic Agent creation wizard", () => {
  const values = {
    name: "Executor",
    handle: "atomic-executor",
    persona: "Careful reviewer",
    model: "gpt-test",
    roles: ["EXECUTOR"],
    authorization_mode: "READ_ONLY",
    system_prompt: "Follow the frozen plan",
    avatar_style: "PIXEL",
    avatar_seed: 9007199254740991,
    skill_ids: ["55"],
    memory_policy: '{"profile":"STANDARD"}',
    default_channels: [" general ", "", "review"],
  }
  it("submits the complete configuration without accepting an identity or exposing credentials", () => {
    const body = buildAgentCreation(
      { ...values, principal_id: "999", client_secret: "untrusted" },
      "401",
      "CODEX"
    )
    expect(body.default_channels).toEqual(["general", "review"])
    expect(body.skill_ids).toEqual(["55"])
    expect(body.avatar_seed).toBe(9007199254740991)
    expect(body.memory_policy).toBe('{"profile":"STANDARD"}')
    expect(body).not.toHaveProperty("principal_id")
    expect(body).not.toHaveProperty("client_secret")
    expect(() =>
      buildAgentCreation(
        { ...values, memory_policy: "invalid JSON" },
        "401",
        "CODEX"
      )
    ).toThrow()
  })
  it("compiles the actual generated request contract and rejects invalid complete submissions", () => {
    const api = JSON.parse(
      readFileSync(
        new URL(
          "../../../../../../backend-api/docs/openapi.json",
          import.meta.url
        ),
        "utf8"
      )
    )
    const path = "/tenants/{tenant_id}/agent-registrations"
    const operation = api.paths[path].post
    const compiled = compileRequestContract(
      {
        version: "v1",
        method: "POST",
        path,
        operation_id: operation.operationId,
        schemas: api.components.schemas,
        request_body: operation.requestBody,
      },
      (key) => key
    )
    expect(compiled.unsupported).toEqual([])
    const body = buildAgentCreation(values, "401", "CODEX")
    expect(compiled.schema.safeParse(body).success).toBe(true)
    expect(
      compiled.schema.safeParse({ ...body, avatar_seed: 9007199254740992 })
        .success
    ).toBe(false)
    expect(
      compiled.schema.safeParse({ ...body, handle: "../outside" }).success
    ).toBe(false)
    expect(
      compiled.schema.safeParse({ ...body, skill_ids: Array(31).fill("55") })
        .success
    ).toBe(false)
  })
})
