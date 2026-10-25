import { describe, expect, it } from "bun:test"
import { channelSettings, intakeChannelsApi } from "./api"
import { intakeChannelMessages } from "./messages"
import type { iamApi } from "../../lib/iam-api"
const values = {
  enabled: false,
  display_name: "Client workspace",
  support_email: "",
  language: "en-US",
  daily_limit: 20,
  receipt_enabled: false,
  reminder_hours: 48,
  timeout_hours: 168,
}
describe("intake channel policy", () => {
  it("preserves exact notification targets and rejects ambiguous recipient lists", () => {
    expect(channelSettings({...values, notify_principal_ids: "9007199254740993, 17\n23"}).notify_principal_ids).toEqual(["9007199254740993", "17", "23"])
    expect(channelSettings({...values, notify_principal_ids: []}).notify_principal_ids).toBeUndefined()
    for (const notify_principal_ids of ["0", "-1", "17,17", "17,1.2", Array.from({length: 51}, (_, index) => String(index + 1))]) {
      expect(() => channelSettings({...values, notify_principal_ids})).toThrow()
    }
  })
  it("rejects invalid limits and reminder ordering", () => {
    expect(
      channelSettings({ ...values, triage_agent_id: "9007199254740993" })
        .triage_agent_id
    ).toBe("9007199254740993")
    for (const patch of [
      { daily_limit: 0 },
      { daily_limit: 1.2 },
      { timeout_hours: 48 },
      { timeout_hours: 9000 },
      { reminder_hours: 0 },
      { triage_agent_id: "-1" },
    ])
      expect(() => channelSettings({ ...values, ...patch })).toThrow()
    expect(channelSettings(values)).toEqual(values)
  })
  it("uses optimistic concurrency and explicit replay keys without pretending to activate ingress", async () => {
    const calls: {
      route: { method: string; url: string }
      init?: Omit<RequestInit, "method">
    }[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(
      route: Parameters<typeof iamApi.deliveryResultRequest>[0],
      init?: Parameters<typeof iamApi.deliveryResultRequest>[1]
    ) => {
      calls.push({ route, init })
      return {} as T
    }
    const api = intakeChannelsApi({ deliveryResultRequest: request })
    await api.save("1", "9007199254740993", "EMAIL", 4, values, "retry-key")
    expect(calls[0]?.route).toEqual({
      method: "PUT",
      url: "/tenants/1/projects/9007199254740993/intake-channels/EMAIL",
    })
    expect(new Headers(calls[0]?.init?.headers).get("If-Match")).toBe('"4"')
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe(
      "retry-key"
    )
    await api.save("1", "99", "API", 0, values, "first-key")
    expect(new Headers(calls[1]?.init?.headers).has("If-Match")).toBe(false)
  })
  it("explains unavailable transports in all three locales", () => {
    for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
      expect(Object.keys(intakeChannelMessages[locale]).sort()).toEqual(
        Object.keys(intakeChannelMessages["en-US"]).sort()
      )
      expect(intakeChannelMessages[locale].unavailable).toContain("UNAVAILABLE")
    }
  })
  it("rotates credentials with the exact version and submits sources with both authorization headers", async () => {
    const calls: { route: { method: string; url: string }; init?: Omit<RequestInit, "method"> }[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => { calls.push({ route, init }); return {} as T }
    const api = intakeChannelsApi({ deliveryResultRequest: request })
    await api.rotateCredential("1", "9007199254740993", 7, "rotation-replay-key")
    expect(calls[0]?.route).toEqual({ method: "POST", url: "/tenants/1/projects/9007199254740993/intake-channels/API/credential" })
    expect(new Headers(calls[0]?.init?.headers).get("If-Match")).toBe('"7"')
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("rotation-replay-key")
    const source = { contact_id: "9007199254740995", title: "Original source", content: "<script>untrusted</script>", format: "PLAIN" as const }
    await api.receive("1", "99", "a".repeat(64), source, "submission-replay-key")
    expect(calls[1]?.route).toEqual({ method: "POST", url: "/tenants/1/projects/99/intake-channels/API/submissions" })
    expect(new Headers(calls[1]?.init?.headers).get("X-Intake-Key")).toBe("a".repeat(64))
    expect(new Headers(calls[1]?.init?.headers).get("Idempotency-Key")).toBe("submission-replay-key")
    expect(JSON.parse(String(calls[1]?.init?.body))).toEqual(source)
    expect(String(calls[1]?.init?.body)).not.toContain("a".repeat(64))
  })
})

it("preserves translated Unicode text", () => {
  expect(intakeChannelMessages["zh-CN"].title).toMatch(/[\u4e00-\u9fff]/)
  for (const labels of Object.values(intakeChannelMessages))
    for (const value of Object.values(labels)) expect(value).not.toContain("??")
})

it("preserves exact callback identifiers and permits clearing the association", () => {
	const values = {display_name:"API", language:"en-US", daily_limit:20, reminder_hours:48, timeout_hours:168}
	expect(channelSettings({...values, callback_subscription_id:"9007199254740993"}).callback_subscription_id).toBe("9007199254740993")
	expect(channelSettings({...values, callback_subscription_id:""}).callback_subscription_id).toBeUndefined()
	for (const id of ["0","-1","1e3","9223372036854775808"]) expect(() => channelSettings({...values, callback_subscription_id:id})).toThrow()
})
