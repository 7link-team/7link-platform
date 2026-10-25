import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"
export type PriceAmounts = {input_micros_per_mtok: string; output_micros_per_mtok: string; cache_read_micros_per_mtok: string; cache_write_micros_per_mtok: string}
export type ModelPrice = PriceAmounts & {id: string; runtime_kind: "CODEX" | "CLAUDE_CODE"; model_id: string; version: number; updated_at: string}
export type ModelPricePage = {items: ModelPrice[]; has_more: boolean; next_cursor?: string}
export const createModelPricesApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
 list: (tenant: string, cursor?: string, signal?: AbortSignal) => api.deliveryResultRequest<ModelPricePage>({method: "GET", url: `/tenants/${collaborationID(tenant)}/model-prices?${new URLSearchParams({limit: "30", ...(cursor ? {cursor} : {})})}`}, {signal}),
 save: (tenant: string, runtime: ModelPrice["runtime_kind"], model: string, version: number, amounts: PriceAmounts) => api.deliveryResultRequest<ModelPrice>({method: "PUT", url: `/tenants/${collaborationID(tenant)}/model-prices/${runtime}/${encodeURIComponent(model)}`}, {headers: {"If-Match": `"${version}"`}, body: JSON.stringify(amounts)}),
 remove: (tenant: string, price: ModelPrice) => api.deliveryResultRequest<void>({method: "DELETE", url: `/tenants/${collaborationID(tenant)}/model-prices/${price.runtime_kind}/${encodeURIComponent(price.model_id)}`}, {headers: {"If-Match": `"${price.version}"`}}),
})
export const modelPricesApi = createModelPricesApi(iamApi)
export const validPriceAmount = (raw: string) => /^(0|[1-9][0-9]{0,18})$/.test(raw) && BigInt(raw) <= 9223372036854775807n
