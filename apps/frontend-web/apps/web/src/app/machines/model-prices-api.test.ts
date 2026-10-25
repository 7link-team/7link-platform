import {expect, test} from "bun:test"
import {createModelPricesApi, validPriceAmount, type ModelPrice} from "./model-prices-api"
test("model prices preserve exact decimal micros, resource identity and version preconditions", async () => {
 const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
 const api = createModelPricesApi({deliveryResultRequest: async <T>(route: {method: string; url: string}, init?: Omit<RequestInit, "method">) => {calls.push({route, init});return {} as T}})
 const amounts = {input_micros_per_mtok: "9007199254740993", output_micros_per_mtok: "17", cache_read_micros_per_mtok: "3", cache_write_micros_per_mtok: "7"}
 await api.save("9007199254740995", "CODEX", "proxy/model", 3, amounts)
 expect(calls[0]?.route.url).toBe("/tenants/9007199254740995/model-prices/CODEX/proxy%2Fmodel")
 expect(calls[0]?.init?.headers).toEqual({"If-Match": '"3"'})
 expect(JSON.parse(String(calls[0]?.init?.body))).toEqual(amounts)
 await api.remove("9007199254740995", {...amounts, runtime_kind: "CODEX", model_id: "proxy/model", version: 4} as ModelPrice)
 expect(calls[1]?.route.method).toBe("DELETE");expect(calls[1]?.init?.headers).toEqual({"If-Match": '"4"'})
 const controller = new AbortController()
 await api.list("9007199254740995", "9007199254740999", controller.signal)
 expect(calls[2]?.init?.signal).toBe(controller.signal);expect(calls[2]?.route.url).toContain("cursor=9007199254740999")
 for (const value of ["0", "9007199254740993", "9223372036854775807"]) expect(validPriceAmount(value)).toBe(true)
 for (const value of ["", "-1", "1.5", "1e3", "01", "9223372036854775808"]) expect(validPriceAmount(value)).toBe(false)
})
