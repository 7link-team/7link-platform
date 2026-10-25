import { describe, expect, it } from "bun:test"
import {
  reviewCreateBody,
  reviewOpinionBody,
  reviewSheetApi,
  type Sheet,
} from "./api"
import { reviewSheetMessages } from "./messages"
it("reads an exact review sheet independently of collection pagination", async () => {
  const calls: { url: string; signal?: AbortSignal | null }[] = []
  const api = reviewSheetApi({
    deliveryResultRequest: async <T>(
      route: { url: string },
      init?: RequestInit
    ) => {
      calls.push({ url: route.url, signal: init?.signal })
      return {} as T
    },
    deliveryResultPage: async () => ({ items: [], total: 0 }),
  })
  const signal = new AbortController().signal
  await api.get("9007199254740993", "9007199254740995", signal)
  expect(calls).toEqual([
    {
      url: "/entities/9007199254740993/review-sheets/9007199254740995",
      signal,
    },
  ])
  await api.get("1/other", "2/other")
  expect(calls[1]?.url).toBe("/entities/1%2Fother/review-sheets/2%2Fother")
})
it("reads bounded immutable opinion history for the exact sheet", async () => {
  const calls: string[] = []
  const api = reviewSheetApi({
    deliveryResultRequest: async <T>(route: { url: string }) => {
      calls.push(route.url)
      return { items: [], has_more: false } as T
    },
    deliveryResultPage: async () => ({ items: [], total: 0 }),
  })
  await api.history("9007199254740993", "9007199254740995", "opinions-7.cursor")
  expect(calls).toEqual([
    "/entities/9007199254740993/review-sheets/9007199254740995/opinions?cursor=opinions-7.cursor&limit=50",
  ])
})
import type { iamApi } from "../../lib/iam-api"
it("preserves work targets and versions without inventing a requirement", () => {
  const body = reviewCreateBody({
    target_kind: "WORK_ITEM",
    target_id: "9007199254740993",
    target_version: 7,
    reviewers: "11",
    minimum: 1,
    review_type: "Design",
  })
  expect(body).toMatchObject({
    work_item_id: "9007199254740993",
    work_item_version: 7,
  })
  expect(body).not.toHaveProperty("requirement_id")
  expect(() =>
    reviewCreateBody({
      target_kind: "WORK_ITEM",
      target_id: "0",
      target_version: 7,
      reviewers: "11",
      minimum: 1,
    })
  ).toThrow()
})
it("addresses project copies separately and retains disabled propagation", async () => {
  const calls: { url: string; body?: unknown }[] = []
  const api = reviewSheetApi({
    deliveryResultPage: async () => {
      throw new Error("offset collection adapter must not be used")
    },
    deliveryResultRequest: async <T>(
      route: { url: string },
      options?: { body?: unknown }
    ) => {
      calls.push({ url: route.url, body: options?.body })
      return {} as T
    },
  })
  await api.types("1", "", undefined, "99")
  await api.saveType(
    "1",
    { id: "7", version: 2, settings: {} as never },
    {
      name: "Local",
      applies_to: { requirement: true, work_item_types: ["TASK"] },
      review_nodes: ["DRAFT"],
      default_reviewer_ids: ["11"],
      min_reviewers: 1,
      content_fields: ["title", "criteria"],
      per_ac_required: true,
      propagate: false,
    },
    "copy-key",
    "99"
  )
  expect(calls[0]?.url).toBe(
    "/tenants/1/review-types?cursor=&limit=50&project_id=99"
  )
  expect(calls[1]?.url).toBe("/tenants/1/review-types/7?project_id=99")
  expect(JSON.parse(String(calls[1]?.body)).settings.propagate).toBe(false)
})
describe("requirement review commands", () => {
  it("preserves snowflake IDs and rejects duplicate reviewers and insufficient minima", () => {
    const values = {
      requirement_id: "9007199254740993",
      requirement_version: 4,
      review_type: "Requirements",
      reviewers: "9007199254740995,9007199254740997",
      minimum: 2,
    }
    expect(reviewCreateBody(values).reviewers).toEqual([
      "9007199254740995",
      "9007199254740997",
    ])
    for (const patch of [
      { reviewers: "11,11" },
      { minimum: 3 },
      { minimum: 1.5 },
      { requirement_version: 0 },
      { requirement_id: "0" },
    ])
      expect(() => reviewCreateBody({ ...values, ...patch })).toThrow()
  })
  it("requires per-criterion rejection feedback and includes the frozen sheet version", () => {
    const sheet = {
      version: 7,
      document: {
        criteria: [{ id: "9007199254740993", code: "AC-1", text: "Output" }],
      },
    } as Sheet
    expect(() => reviewOpinionBody(sheet, { decision: "REJECT" })).toThrow()
    expect(
      reviewOpinionBody(sheet, {
        decision: "REJECT",
        criterion_9007199254740993: "Mismatch",
        criterion_other: "ignored",
      })
    ).toEqual({
      version: 7,
      action: "OPINION",
      opinion: {
        decision: "REJECT",
        comment: "",
        criteria: { "9007199254740993": "Mismatch" },
      },
    })
  })
  it("sends commands to the actual scoped resource and preserves caller replay keys", async () => {
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
    const api = reviewSheetApi({
      deliveryResultRequest: request,
      deliveryResultPage: async () => ({ items: [], total: 0 }),
    })
    const body = reviewCreateBody({
      requirement_id: "99",
      requirement_version: 1,
      review_type: "Review",
      reviewers: "11",
      minimum: 1,
    })
    await api.create("9007199254740993", body, "same-key")
    await api.action(
      "9007199254740993",
      "9007199254740995",
      { version: 1, action: "CANCEL", reason: "Superseded" },
      "same-action-key"
    )
    expect(calls[0]?.route.url).toBe("/entities/9007199254740993/review-sheets")
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe(
      "same-key"
    )
    expect(new Headers(calls[1]?.init?.headers).get("If-Match")).toBe('"1"')
    expect(calls[1]?.route.url).toEndWith(
      "/review-sheets/9007199254740995/actions"
    )
  })
  it("keeps every page label translated", () => {
    for (const locale of ["zh-CN", "ms-MY"] as const) {
      expect(Object.keys(reviewSheetMessages[locale]).sort()).toEqual(
        Object.keys(reviewSheetMessages["en-US"]).sort()
      )
      for (const value of Object.values(reviewSheetMessages[locale]))
        expect(value.trim().length).toBeGreaterThan(0)
    }
  })
})

it("preserves translated Unicode text", () => {
  expect(reviewSheetMessages["zh-CN"].title).toMatch(/[\u4e00-\u9fff]/)
  for (const labels of Object.values(reviewSheetMessages))
    for (const value of Object.values(labels)) expect(value).not.toContain("??")
})

it("uses bounded cursor collection responses without numeric ID coercion", async () => {
  const calls: { url: string; signal?: AbortSignal | null }[] = []
  const signal = new AbortController().signal
  const page = {
    items: [
      {
        id: "9007199254740993",
        requirement_id: "7",
        initiator_id: "11",
        state: "OPEN",
        version: 1,
        document: {
          title: "Review",
          review_type: "Review",
          requirement_version: 1,
          baseline_version: 1,
          reviewers: ["11"],
          minimum: 1,
          criteria: [],
          opinions: [],
          rework_ids: [],
        },
      },
    ],
    has_more: true,
    next_cursor: "sheets.scope.9007199254740993",
  }
  const api = reviewSheetApi({
    deliveryResultRequest: async <T>(
      route: { url: string },
      init?: RequestInit
    ) => {
      calls.push({ url: route.url, signal: init?.signal })
      return page as T
    },
    deliveryResultPage: async () => {
      throw new Error("offset adapter")
    },
  })
  expect(await api.list("99", "sheets.a+/=", signal)).toEqual(page)
  expect(calls[0]).toEqual({
    url: "/entities/99/review-sheets?cursor=sheets.a%2B%2F%3D&limit=50",
    signal,
  })
  await api.types("1", "types.a", signal, "99")
  await api.history("99", "9007199254740993", "opinions-a.b", signal)
  expect(calls.map((c) => c.url)).toEqual([
    "/entities/99/review-sheets?cursor=sheets.a%2B%2F%3D&limit=50",
    "/tenants/1/review-types?cursor=types.a&limit=50&project_id=99",
    "/entities/99/review-sheets/9007199254740993/opinions?cursor=opinions-a.b&limit=50",
  ])
})
