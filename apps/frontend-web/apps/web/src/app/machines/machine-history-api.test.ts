import {expect, test} from "bun:test"
import {createMachineHistoryApi} from "./machine-history-api"
test("machine history requests bind tenant, exact machine and server continuation, with cancellation", async () => {
 const calls: {url: string; signal?: AbortSignal | null}[] = []
 const api = createMachineHistoryApi({deliveryResultRequest: async <T>(route: {method: string; url: string}, init?: Omit<RequestInit, "method">) => {calls.push({url: route.url, signal: init?.signal});return {} as T}})
 const controller = new AbortController()
 await api.agents("9007199254740993", "9007199254740995", "opaque/+", controller.signal)
 expect(calls[0]?.url).toContain("/tenants/9007199254740993/agents?");expect(calls[0]?.url).toContain("machine_id=9007199254740995");expect(calls[0]?.url).toContain("cursor=opaque%2F%2B");expect(calls[0]?.signal).toBe(controller.signal)
 await api.executions("9007199254740995", "opaque/+", controller.signal)
 expect(calls[1]?.url).toContain("/machines/9007199254740995/execution-history?");expect(calls[1]?.url).toContain("cursor=opaque%2F%2B");expect(calls[1]?.signal).toBe(controller.signal)
})
