import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"

const sources = readFileSync(new URL("./sources.tsx", import.meta.url), "utf8")
const machines = readFileSync(new URL("../agents/tool-catalog.tsx", import.meta.url), "utf8").replace(/\r\n/g, "\n")
const chunks = readFileSync(new URL("./source.tsx", import.meta.url), "utf8")

type Page = {items: {id: string}[]; next_cursor?: string; has_more?: boolean; next_offset?: number}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no})
  return {promise, resolve, reject}
}

// Execute the production event handlers without mocking React globally. Deferred
// API responses deliberately ignore abort, so cancellation must also fence commits.
function harness(source: string, kind: "sources" | "machines" | "chunks") {
  const body = source.match(/const more = async \(\) => \{\n([\s\S]*?)\n {2}\}/)?.[1]
  if (!body) throw Error("Production paging handler missing")
  const paging: {current: AbortController | null} = {current: null}
  const calls: {signal: AbortSignal; response: ReturnType<typeof deferred<Page>>}[] = []
  let rows = [{id: "first"}]
  let cursor = "first-cursor"
  let next: string | null = "chunk-cursor"
  let busy = false
  let error = ""
  const request = (...args: unknown[]) => {
    const signal = args[args.length - 1] as AbortSignal
    const response = deferred<Page>()
    calls.push({signal, response})
    return response.promise
  }
  const setRows = (update: (old: typeof rows) => typeof rows) => {rows = update(rows)}
  const bindings = {
    paging, loading: false, busy: false, cursor, next, tenant: "7", sourceID: "8", query: {state: "INDEXED"},
    knowledgeSources: {list: request, chunkPage: request}, iamApi: {machines: request},
    setLoading: (value: boolean) => {busy = value},
    setPagingBusy: (value: boolean) => {busy = value},
    setItems: setRows, setMachines: setRows, setChunks: setRows,
    setCursor: (value: string) => {cursor = value},
    setNext: (value: string | null) => {next = value},
    setError: (value: string) => {error = value}, t: (key: string) => key,
  }
  const more = new Function(...Object.keys(bindings), `return async () => {${body}}`)(...Object.values(bindings)) as () => Promise<void>
  const cleanupBody = source.match(/return \(\) => \{controller.abort\(\); paging.current\?\.abort\(\); paging.current = null\}/)?.[0]
  if (!cleanupBody) throw Error("Production paging cleanup missing")
  const cleanup = new Function("controller", "paging", cleanupBody)(new AbortController(), paging) as () => void
  const refreshBody = (kind === "chunks" ? source : sources).match(/const refresh = \(\) => \{\n([\s\S]*?)\n {2}\}/)?.[1]
  if (!refreshBody) throw Error("Production source refresh handler missing")
  let revisions = 0
  const refresh = new Function("paging", "setRevision", "setPagingBusy", `return () => {${refreshBody}}`)(paging, (update: (value: number) => number) => {revisions = update(revisions)}, bindings.setPagingBusy) as () => void
  return {kind, more, cleanup, refresh, calls, paging, replaceRows: (next: typeof rows) => {rows = next}, snapshot: () => ({rows, cursor, next, busy, error, revisions})}
}

describe("deferred source and machine paging", () => {
  for (const [kind, source] of [["sources", sources], ["machines", machines]] as const) {
    it(`${kind}: sends one same-cursor request and appends its result once`, async () => {
      const page = harness(source, kind)
      const first = page.more()
      const duplicate = page.more()
      expect(page.calls).toHaveLength(1)
      expect(page.snapshot().busy).toBe(true)
      page.calls[0]!.response.resolve({items: [{id: "next"}], next_cursor: "next-cursor"})
      await Promise.all([first, duplicate])
      expect(page.snapshot()).toMatchObject({rows: [{id: "first"}, {id: "next"}], cursor: "next-cursor", busy: false, error: ""})
    })
    it(`${kind}: aborts on unmount and ignores a late successful response`, async () => {
      const page = harness(source, kind)
      const pending = page.more()
      page.cleanup()
      expect(page.calls[0]!.signal.aborted).toBe(true)
      page.calls[0]!.response.resolve({items: [{id: "old-scope"}], next_cursor: "old-cursor"})
      await pending
      expect(page.snapshot().rows).toEqual([{id: "first"}])
      expect(page.snapshot().cursor).toBe("first-cursor")
    })
    it(`${kind}: releases a failed request so the same page can be retried`, async () => {
      const page = harness(source, kind)
      const failed = page.more()
      page.calls[0]!.response.reject(Error("temporarily offline"))
      await failed
      expect(page.snapshot()).toMatchObject({cursor: "first-cursor", busy: false, error: "temporarily offline"})
      const retry = page.more()
      expect(page.calls).toHaveLength(2)
      expect(page.calls[1]!.signal.aborted).toBe(false)
      page.calls[1]!.response.resolve({items: [{id: "retry-result"}]})
      await retry
      expect(page.snapshot()).toMatchObject({rows: [{id: "first"}, {id: "retry-result"}], cursor: "", busy: false, error: ""})
    })
  }
  it("source refresh cancels old paging and its completion cannot release a newer request", async () => {
    const page = harness(sources, "sources")
    const old = page.more()
    page.refresh()
    expect(page.calls[0]!.signal.aborted).toBe(true)
    expect(page.snapshot().revisions).toBe(1)
    page.replaceRows([{id: "refreshed"}])
    const current = page.more()
    const currentController = page.paging.current
    page.calls[0]!.response.resolve({items: [{id: "stale"}], next_cursor: "stale-cursor"})
    await old
    expect(page.snapshot().rows).toEqual([{id: "refreshed"}])
    expect(page.paging.current).toBe(currentController)
    expect(page.snapshot().busy).toBe(true)
    page.calls[1]!.response.resolve({items: [{id: "current"}], next_cursor: "current-cursor"})
    await current
    expect(page.snapshot()).toMatchObject({rows: [{id: "refreshed"}, {id: "current"}], cursor: "current-cursor", busy: false})
  })
  it("source state filter aborts paging before changing the route", async () => {
    const page = harness(sources, "sources")
    const pending = page.more()
    const body = sources.match(/onChange=\{event => \{(paging.current\?\.abort\(\);[^\n]+?)\}\}/)?.[1]
    if (!body) throw Error("Production filter handler missing")
    let selected: unknown
    const change = new Function("paging", "setSearch", `return event => {${body}}`)(page.paging, (value: unknown) => {expect(page.calls[0]!.signal.aborted).toBe(true); selected = value}) as (event: {target: {value: string}}) => void
    change({target: {value: "FAILED"}})
    expect(selected).toEqual({state: "FAILED"})
    page.replaceRows([{id: "failed-first"}])
    page.calls[0]!.response.resolve({items: [{id: "indexed-late"}], next_cursor: "indexed-cursor"})
    await pending
    expect(page.snapshot().rows).toEqual([{id: "failed-first"}])
    expect(page.snapshot().cursor).toBe("first-cursor")
  })
})

describe("source detail chunk paging", () => {
  it("keeps knowledge locale keys aligned and rejects missing or corrupted translations", () => {
    expect(sources).toContain('t("knowledgeSources")')
    // The original source/catalog translations now span capability namespaces.
    const capabilityKeys = [
      "knowledgeQueueSource",
      "knowledgeUsageInclusions",
      "knowledgeUsageCitations",
      "knowledgeUsageUnavailable",
      "knowledgeSourceReason_INDEX_AUTHORITY_REVOKED",
      "knowledgeSourceReason_INGESTION_RETRIES_EXHAUSTED",
      "reviewSheet",
      "intakeChannels",
      "knowledgeSettings",
      "knowledgeSettingsHint",
      "knowledgeRetrievalEngine",
      "knowledgeDatabaseDialect",
      "knowledgeFullText",
      "knowledgeHybrid",
      "knowledgeRemoteSync",
      "knowledgeCapabilityHint",
      "knowledgeCandidateLimit",
      "knowledgeSources",
      "knowledgeSource",
      "knowledgeSourcesHint",
      "knowledgeChunkCount",
      "knowledgeBindSource",
      "knowledgeSourceUploadHint",
      "knowledgeSourceLocator",
      "knowledgeSourceFormat",
      "knowledgeSourceContent",
      "knowledgeSourceKind_UPLOAD",
      "knowledgeSourceKind_URL",
      "knowledgeSourceKind_REPOSITORY",
      "knowledgeSourceState_PENDING",
      "knowledgeSourceState_INDEXING",
      "knowledgeSourceState_INDEXED",
      "knowledgeSourceState_FAILED",
      "knowledgeSourceState_INVALIDATED",
      "knowledgeSourceScopeMismatch",
      "knowledgeChunks",
      "knowledgeTextChars",
      "knowledgeIndexAttempts",
      "knowledgeIndexedAt",
      "knowledgeSourceReason",
      "knowledgeSourceReason_UNSUPPORTED_SOURCE",
      "knowledgeSourceReason_PDF_EXTRACTION_UNAVAILABLE",
      "knowledgeSourceReason_TEXT_NOT_EXTRACTABLE",
      "knowledgeSourceReason_TOO_LARGE",
      "knowledgeSourceReason_CODE_LINE_TOO_LONG",
      "knowledgeSourceReason_TABLE_ROW_TOO_LONG",
      "knowledgeSourceReason_TOO_MANY_CHUNKS",
      "knowledgeSourceReason_MANUALLY_INVALIDATED",
      "knowledgeSyncSource",
      "knowledgeReindexSource",
      "knowledgeReplaceSource",
      "knowledgeInvalidateConfirm",
      "knowledgeInvalidateSource",
      "knowledgeChunkSearch",
      "knowledgeTokenEstimateHint",
      "knowledgeTokenEstimate",
      "toolCatalogTitle",
      "toolCatalogMachineTools",
      "capabilityPermissionRequired",
      "toolCatalogFactsOnly",
      "toolCatalogSelectMachine",
      "toolCatalogLocalInventory",
      "knowledgeSourceKind_REQUIREMENT",
      "knowledgeSourceKind_FEEDBACK",
      "knowledgeSourceKind_DECISION",
      "knowledgeSourceKind_DELIVERY",
    ]
    let expectedKeys: string[] | undefined
    for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
      const dictionary = JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`, import.meta.url), "utf8")) as Record<string, string>
      const keys = capabilityKeys.filter(key => Object.hasOwn(dictionary, key)).sort()
      expect(keys).toEqual([...capabilityKeys].sort())
      expect(keys.length).toBeGreaterThan(0)
      if (expectedKeys) expect(keys).toEqual(expectedKeys)
      expectedKeys = keys
      for (const key of keys) {
        const value = dictionary[key]!
        expect(value.trim().length).toBeGreaterThan(0)
        expect(value).not.toContain("?")
        expect(value).not.toContain("\uFFFD")
        if (locale === "zh-CN") expect(value).toMatch(/[\u3400-\u9fff]/)
      }
    }
  })
  it("guards simultaneous same-cursor requests and advances once", async () => {
    const page = harness(chunks, "chunks")
    const first = page.more()
    const duplicate = page.more()
    expect(page.calls).toHaveLength(1)
    expect(page.calls[0]!.signal.aborted).toBe(false)
    page.calls[0]!.response.resolve({items: [{id: "page2"}], has_more: true, next_cursor: "chunk-next"})
    await Promise.all([first, duplicate])
    expect(page.snapshot()).toMatchObject({rows: [{id: "first"}, {id: "page2"}], next: "chunk-next", busy: false})
  })
  for (const cancel of ["cleanup", "refresh"] as const) {
    it(`ignores a late chunk page after ${cancel}`, async () => {
      const page = harness(chunks, "chunks")
      const pending = page.more()
      page[cancel]()
      expect(page.calls[0]!.signal.aborted).toBe(true)
      page.replaceRows([{id: "new-snapshot"}])
      page.calls[0]!.response.resolve({items: [{id: "old-page"}], has_more: true, next_cursor: "old-chunk-cursor"})
      await pending
      expect(page.snapshot()).toMatchObject({rows: [{id: "new-snapshot"}], next: "chunk-cursor"})
    })
  }
  it("retains a failed cursor for retry and reaches the final page", async () => {
    const page = harness(chunks, "chunks")
    const failed = page.more()
    page.calls[0]!.response.reject(Error("network unavailable"))
    await failed
    expect(page.snapshot()).toMatchObject({next: "chunk-cursor", busy: false, error: "network unavailable"})
    const retry = page.more()
    expect(page.calls).toHaveLength(2)
    page.calls[1]!.response.resolve({items: [{id: "last"}], has_more: false})
    await retry
    expect(page.snapshot()).toMatchObject({rows: [{id: "first"}, {id: "last"}], next: null, busy: false, error: ""})
  })
  it("an aborted old rejection cannot clear a newer paging request or show its error", async () => {
    const page = harness(chunks, "chunks")
    const old = page.more()
    page.refresh()
    const current = page.more()
    const controller = page.paging.current
    page.calls[0]!.response.reject(Error("old failure"))
    await old
    expect(page.paging.current).toBe(controller)
    expect(page.snapshot()).toMatchObject({busy: true, error: ""})
    page.calls[1]!.response.resolve({items: [{id: "current"}], has_more: false})
    await current
    expect(page.snapshot()).toMatchObject({rows: [{id: "first"}, {id: "current"}], next: null, busy: false})
  })
  it("translates every actual backend source reason in all locales and preserves unknown codes", () => {
    const backend = readFileSync(new URL("../../../../../../backend-api/internal/knowledge/source.go", import.meta.url), "utf8") + readFileSync(new URL("../../../../../../backend-api/internal/knowledge/source_chunk.go", import.meta.url), "utf8")
    const reasons = new Set([...backend.matchAll(/(?:StateReason =|reason =|return nil,) "([A-Z_]+)"/g)].map(match => match[1]!))
    const jobs = readFileSync(new URL("../../../../../../backend-api/internal/knowledge/source_jobs.go", import.meta.url), "utf8")
    for (const match of jobs.matchAll(/failSourceJob\(ctx, job, payload, "([A-Z_]+)"\)/g)) reasons.add(match[1]!)
    const entries = chunks.match(/const sourceReasons = new Map\((\[[\s\S]*?\])\)/)?.[1]
    if (!entries) throw Error("Production reason map missing")
    const messages = new Map<string, string>(new Function(`return ${entries}`)())
    expect(reasons.size).toBe(10)
    for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
      const dictionary = JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`, import.meta.url), "utf8")) as Record<string, string>
      for (const reason of reasons) {
        const key = messages.get(reason)
        expect(key).toBeDefined()
        expect(dictionary[key!]?.trim().length).toBeGreaterThan(0)
        expect(dictionary[key!]).not.toContain("??")
        expect(dictionary[key!]).not.toBe(reason)
      }
    }
    expect(messages.get("FUTURE_REASON")).toBeUndefined()
    expect(chunks).toContain("reasonKey ? t(reasonKey) : source.state_reason")
  })
})
