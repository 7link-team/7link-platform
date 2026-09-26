import { afterEach, describe, expect, it } from "bun:test"
import {
  ApiError,
  createIamApi,
  clientErrorMessage,
  fileOperationConflicts,
  shouldNotifyMenuCatalogChange,
  type AuditEvent,
  type DepartmentInput,
  type FileOperationConflicts,
  type MemberAuthorizationSnapshot,
  type OAuthClientInput,
} from "./iam-api"

interface RecordedRequest {
  method: string
  path: string
  headers: Headers
  body: string
}

describe("IAM API client", () => {
  it("separates identity, current browser session and session item addresses", async () => {
    const requests: string[] = []
    server = Bun.serve({
      port: 0,
      fetch(request) {
        const url = new URL(request.url)
        requests.push(`${request.method} ${url.pathname}`)
        return Response.json({ code: 0, data: {} })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "90",
      () => "101"
    )
    const id = "a".repeat(64)
    await client.session()
    await client.currentBrowserSession()
    await client.browserSession(id)
    await client.revokeSession(id)
    await client.logout()
    expect(requests).toEqual([
      "GET /me",
      "GET /sessions/current",
      `GET /sessions/${id}`,
      `DELETE /sessions/${id}`,
      "DELETE /sessions/current",
    ])
  })
  let server: ReturnType<typeof Bun.serve> | undefined
  afterEach(() => server?.stop(true))

  it("discovers and caches the principal, then clears it after signing out all devices", async () => {
    const requests: string[] = []
    let subject = "101"
    server = Bun.serve({
      port: 0,
      fetch(request) {
        const path = new URL(request.url).pathname
        requests.push(path)
        return Response.json({
          code: 0,
          data: path === "/me" ? { subject } : [],
        })
      },
    })
    const client = createIamApi(`http://127.0.0.1:${server.port}`, () => "90")
    await Promise.all([client.sessions(), client.passkeys()])
    expect(requests.filter((path) => path === "/me")).toHaveLength(1)
    expect(requests).toContain("/principals/101/sessions")
    expect(requests).toContain("/principals/101/passkeys")
    await client.logoutAll()
    subject = "202"
    await client.sessions()
    expect(requests.filter((path) => path === "/me")).toHaveLength(2)
    expect(requests.at(-1)).toBe("/principals/202/sessions")
  })

  it("uses the verified login subject for restricted MFA enrollment", async () => {
    const requests: string[] = []
    server = Bun.serve({
      port: 0,
      fetch(request) {
        const path = new URL(request.url).pathname
        requests.push(path)
        if (path === "/me") return Response.json({ code: 401 }, { status: 401 })
        return Response.json({
          code: 0,
          data:
            path === "/login"
              ? { status: "mfa_enrollment_required", subject: "202" }
              : {},
        })
      },
    })
    const client = createIamApi(`http://127.0.0.1:${server.port}`)
    await client.login({
      login_name: "new-user",
      password: "password",
      return_url: "/",
    })
    await client.beginTotpEnrollment("password")
    await client.confirmTotpEnrollment("123456")
    expect(requests).toEqual([
      "/login",
      "/principals/202/mfa/totp-enrollments",
      "/principals/202/mfa/totp",
    ])
  })

  it("rejects an old identity discovery after a new login and retries failed discovery", async () => {
    let resolveIdentity!: (response: Response) => void
    let discoveries = 0
    const requests: string[] = []
    server = Bun.serve({
      port: 0,
      fetch(request) {
        const path = new URL(request.url).pathname
        requests.push(path)
        if (path === "/me") {
          discoveries++
          if (discoveries === 1)
            return new Promise<Response>((resolve) => {
              resolveIdentity = resolve
            })
          if (discoveries === 2)
            return Response.json({ code: 503 }, { status: 503 })
          return Response.json({ code: 0, data: { subject: "202" } })
        }
        return Response.json({ code: 0, data: {} })
      },
    })
    const client = createIamApi(`http://127.0.0.1:${server.port}`)
    const old = client.sessions().catch((error) => error)
    while (!resolveIdentity) await Bun.sleep(1)
    await client.login({
      login_name: "other",
      password: "password",
      return_url: "/",
    })
    resolveIdentity(Response.json({ code: 0, data: { subject: "101" } }))
    expect(await old).toBeInstanceOf(ApiError)
    await expect(client.sessions()).rejects.toBeInstanceOf(ApiError)
    await client.sessions()
    expect(requests).not.toContain("/principals/101/sessions")
    expect(requests.at(-1)).toBe("/principals/202/sessions")
  })

  it("binds file pages to the selected tenant and entity with bounded cursors", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({
          code: 0,
          data:
            url.pathname === "/tenants/90/entities/80/files"
              ? { items: [], next_after: "" }
              : { path: [], items: [], next_after: "" },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "90",
      () => "101"
    )
    await client.files("80", "12")
    await client.fileItems("80", "node/id", "34")

    expect(requests.map((request) => request.path)).toEqual([
      "/entities/80/files?limit=100&after=12",
      "/files/node%2Fid/items?limit=100&after=34",
    ])
    for (const request of requests) {
      expect(request.method).toBe("GET")
      expect(request.headers.get("x-tenant-id")).toBeNull()
      expect(request.headers.get("x-entity-id")).toBeNull()
    }
  })

  it("sends the basic file lifecycle with entity scope and browser multipart boundaries", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        if (url.pathname.endsWith("/content"))
          return new Response("payload", {
            headers: { "content-type": "application/octet-stream" },
          })
        return Response.json({ code: 0, data: { id: "7" } })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "90",
      () => "101"
    )
    await client.createAccountFileRoot()
    await client.createDirectory("80", "1/2", "Reports")
    await client.uploadFile(
      "80",
      "1/2",
      new File(["payload"], "report.txt", { type: "text/plain" })
    )
    expect(await (await client.downloadFile("80", "3/4")).text()).toBe(
      "payload"
    )
    await client.fileByPath("80", "1/2", "Reports/report.txt")
    await client.renameFileEntry("80", "3/4", 5, "renamed.txt")
    await client.renameFileEntry("80", "5/6", 7, "renamed")
    await client.trashFileEntry("80", "3/4", 6)
    await client.trashFileEntry("80", "5/6", 8)
    await client.restoreFileEntry("80", "3/4", 7)
    await client.restoreFileEntry("80", "5/6", 9, "restored.txt")

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /principals/101/files",
      "POST /entities/80/files",
      "POST /entities/80/files",
      "GET /files/3%2F4/content",
      "GET /files/1%2F2/-/Reports/report.txt",
      "PATCH /files/3%2F4",
      "PATCH /files/5%2F6",
      "DELETE /files/3%2F4?revision=6",
      "DELETE /files/5%2F6?revision=8",
      "PATCH /files/3%2F4",
      "PATCH /files/5%2F6",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(requests.every(({ headers }) => !headers.has("x-entity-id"))).toBe(
      true
    )
    for (const request of requests.slice(0, 3))
      expect(request.headers.get("content-type")).toStartWith(
        "multipart/form-data; boundary="
      )
    expect(requests[0]?.body).toContain('name="scope"')
    expect(requests[0]?.body).toContain("account")
    expect(requests[1]?.body).toContain('name="name"')
    expect(requests[1]?.body).toContain("Reports")
    expect(requests[2]?.body).toContain('filename="report.txt"')
    expect(requests[5]?.body).toBe('{"name":"renamed.txt","revision":5}')
    expect(requests[6]?.body).toBe('{"name":"renamed","revision":7}')
    expect(requests[9]?.body).toBe('{"state":"active","revision":7}')
    expect(requests[10]?.body).toBe(
      '{"state":"active","revision":9,"name":"restored.txt"}'
    )
  })

  it("uses a tenant resource path when no entity is selected", async () => {
    const requests: string[] = []
    const server = Bun.serve({
      port: 0,
      fetch(request) {
        const url = new URL(request.url)
        requests.push(url.pathname + url.search)
        return Response.json({ code: 0, data: { items: [] } })
      },
    })
    try {
      const client = createIamApi(
        `http://127.0.0.1:${server.port}`,
        () => "90",
        () => "101"
      )
      await client.files(undefined)
      expect(requests).toEqual(["/tenants/90/files?limit=100"])
    } finally {
      server.stop(true)
    }
  })

  it("preserves file operation conflicts and sends explicit retry decisions", async () => {
    const requests: RecordedRequest[] = []
    const conflict: FileOperationConflicts = {
      space_revision: 7,
      conflicts: [
        {
          key: "a".repeat(64),
          kind: "file",
          source: { kind: "file", name: "report.txt", revision: 2 },
          target: { kind: "file", name: "report.txt", revision: 3 },
          parent: { node_id: "10" },
          policies: ["overwrite", "skip", "rename"],
        },
      ],
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        if (requests.length === 1)
          return Response.json(
            { code: 409, message: "file_operation_conflict", data: conflict },
            { status: 409 }
          )
        return Response.json({ code: 0, data: { created: 1 } })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "90",
      () => "101"
    )
    const input = {
      action: "copy" as const,
      destination_id: "10",
      sources: ["11"],
    }
    let caught: unknown
    try {
      await client.createFileOperation(undefined, input, "operation-key")
    } catch (cause) {
      caught = cause
    }
    expect(caught).toBeInstanceOf(ApiError)
    expect(fileOperationConflicts(caught)).toEqual(conflict)

    await client.createFileOperation(
      undefined,
      {
        ...input,
        observed_revision: 7,
        decisions: [{ conflict_key: "a".repeat(64), policy: "skip" }],
      },
      "operation-key"
    )

    expect(requests.map((request) => request.path)).toEqual([
      "/tenants/90/file-operations",
      "/tenants/90/file-operations",
    ])
    expect(
      requests.map((request) => request.headers.get("idempotency-key"))
    ).toEqual(["operation-key", "operation-key"])
    expect(JSON.parse(requests[0]!.body)).toEqual({
      ...input,
      file_policy: "ask",
      directory_policy: "ask",
      type_policy: "ask",
    })
    expect(JSON.parse(requests[1]!.body)).toEqual({
      ...input,
      observed_revision: 7,
      decisions: [{ conflict_key: "a".repeat(64), policy: "skip" }],
      file_policy: "ask",
      directory_policy: "ask",
      type_policy: "ask",
    })
  })

  it("does not invalidate the menu catalog after file mutations", async () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, "window")
    const browser = new EventTarget()
    let notifications = 0
    browser.addEventListener("menu-catalog-change", () => notifications++)
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: browser,
    })
    server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json({ code: 0, data: { id: "7" } })
      },
    })
    try {
      const client = createIamApi(
        `http://127.0.0.1:${server.port}`,
        () => "90",
        () => "101"
      )
      await client.createDirectory("80", "1", "Reports")
      await client.createFileOperation(
        "80",
        { action: "copy", destination_id: "1", sources: ["2"] },
        "operation-key"
      )
      expect(notifications).toBe(0)

      await client.createTenant({
        slug: "acme",
        name: "Acme",
        owner_principal_id: "principal-1",
      })
      expect(notifications).toBe(1)
    } finally {
      if (previous) Object.defineProperty(globalThis, "window", previous)
      else Reflect.deleteProperty(globalThis, "window")
    }
  })

  it("classifies file paths as mutation-local invalidations", () => {
    expect(shouldNotifyMenuCatalogChange("/files/7?revision=2", "DELETE")).toBe(
      false
    )
    expect(shouldNotifyMenuCatalogChange("/entities/80/files", "POST")).toBe(
      false
    )
    expect(
      shouldNotifyMenuCatalogChange(
        "/tenants/90/entities/80/files/7?revision=2",
        "DELETE"
      )
    ).toBe(false)
    expect(
      shouldNotifyMenuCatalogChange(
        "/tenants/90/entities/80/file-operations",
        "POST"
      )
    ).toBe(false)
    expect(shouldNotifyMenuCatalogChange("/tenants/90/roles", "PATCH")).toBe(
      true
    )
    expect(shouldNotifyMenuCatalogChange("/tenants/files/roles", "PATCH")).toBe(
      true
    )
  })

  it("uses the same file resources without tenant selectors for account files", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        if (url.pathname.endsWith("/content")) return new Response("account")
        return Response.json({ code: 0, data: { items: [], id: "7" } })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "selected-tenant-must-not-leak",
      () => "101"
    )
    await client.files(null)
    await client.createAccountFileRoot()
    await client.fileItems(null, "7")
    await client.createDirectory(null, "7", "Private")
    await client.uploadFile(null, "7", new File(["x"], "private.txt"))
    await client.downloadFile(null, "8")
    await client.renameFileEntry(null, "8", 1, "renamed.txt")
    await client.trashFileEntry(null, "8", 2)

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /principals/101/files?limit=100",
      "POST /principals/101/files",
      "GET /files/7/items?limit=100",
      "POST /principals/101/files",
      "POST /principals/101/files",
      "GET /files/8/content",
      "PATCH /files/8",
      "DELETE /files/8?revision=2",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(requests.every(({ headers }) => !headers.has("x-entity-id"))).toBe(
      true
    )
    expect(requests[1]?.body).toContain("account")
  })

  it("bounds preview downloads before buffering the response", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return new Response("0123456789", {
          headers: {
            "content-length": "10",
            "content-type": "text/plain",
          },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "90",
      () => "101"
    )
    await expect(client.previewFile("80", "3/4", 9)).rejects.toMatchObject({
      status: 413,
      code: "file_preview_too_large",
    })
    expect(await (await client.previewFile("80", "3/4", 10)).text()).toBe(
      "0123456789"
    )
    expect(requests).toHaveLength(2)
    for (const request of requests) {
      expect(request.path).toBe("/files/3%2F4/content")
      expect(request.headers.get("x-tenant-id")).toBeNull()
      expect(request.headers.get("x-entity-id")).toBeNull()
    }
  })

  it("keeps authentication global and explicitly scopes display sessions", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: "",
        })
        return Response.json({ code: 0, data: { subject: "person-1" } })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "unrelated-tenant",
      () => "101"
    )
    await client.session()
    await client.sessionForTenant("authorized-tenant")
    expect(requests.every(({ path }) => path === "/me")).toBe(true)
    expect(requests[0]?.headers.has("x-tenant-id")).toBe(false)
    expect(requests[1]?.headers.get("x-tenant-id")).toBeNull()
  })

  it("keeps personal proofs, lifecycle versions and pagination outside the selected tenant context", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({
          code: 0,
          data:
            url.pathname === "/principals/101/personal-tokens" &&
            request.method === "GET"
              ? []
              : {},
          meta: { page: { total: 47 } },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "unrelated-selected-tenant",
      () => "101"
    )
    expect(await client.personalTokens(20, 20)).toEqual({
      items: [],
      total: 47,
    })
    await client.personalTokenPermissions("71")
    const token = {
      name: "Job token",
      tenant_id: "71",
      permissions: ["user_view"],
      expires_at: "2027-01-01T00:00:00Z",
    }
    await client.preparePersonalToken(token)
    await client.createReauthenticationChallenge({
      purpose: "personal_token_create",
      target: "bound-target",
      method: "password",
    })
    await client.verifyReauthentication({
      proof: "proof",
      password: "password",
      code: "123456",
    })
    await client.createPersonalToken({ ...token, proof: "proof" })
    await client.revokePersonalToken("token/id")
    await client.personalMemberships(50, 50)
    await client.leaveMembership("71", "leave-proof")
    await client.submitClosureRequest("close-proof", 3)
    await client.cancelClosureRequest(4)
    await client.closureRequests(50, 50, "processing")
    await client.processClosureRequest("81", 5)
    await client.rejectClosureRequest("82", 6)
    await client.createIdentityLinkTransaction({
      provider_id: "provider",
      proof: "federation-proof",
      existing_link_id: "existing",
      return_url: "https://console.example.test/account/tokens",
    })
    await client.deleteIdentityLink("provider/link", "unlink-proof")
    expect(
      requests.every(
        ({ headers }) =>
          !headers.has("x-tenant-id") && !headers.has("authorization")
      )
    ).toBe(true)
    expect(requests[0]?.path).toBe(
      "/principals/101/personal-tokens?offset=20&limit=20"
    )
    expect(requests[1]?.path).toBe(
      "/principals/101/personal-token-permissions?tenant_id=71"
    )
    expect(JSON.parse(requests[5]!.body)).toEqual({ ...token, proof: "proof" })
    expect(requests[6]?.path).toBe("/principals/101/personal-tokens/token%2Fid")
    expect(requests[7]?.path).toBe(
      "/principals/101/memberships?offset=50&limit=50"
    )
    expect(JSON.parse(requests[8]!.body)).toEqual({ proof: "leave-proof" })
    expect(JSON.parse(requests[9]!.body)).toEqual({
      proof: "close-proof",
      version: 3,
    })
    expect(requests[10]?.method).toBe("DELETE")
    expect(JSON.parse(requests[10]!.body)).toEqual({ version: 4 })
    expect(requests[11]?.path).toBe(
      "/platform/closure-requests?status=processing&offset=50&limit=50"
    )
    expect(JSON.parse(requests[12]!.body)).toEqual({
      status: "processing",
      version: 5,
    })
    expect(requests[15]?.path).toBe(
      "/principals/101/identity-links/provider%2Flink"
    )
    expect(JSON.parse(requests[15]!.body)).toEqual({ proof: "unlink-proof" })
  })

  it("discovers personal tenants and manages consent without a selected tenant header", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: "",
        })
        return Response.json({
          code: 0,
          data: request.method === "DELETE" ? { revoked: true } : [],
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "stale-tenant",
      () => "101"
    )
    expect(await client.myTenants()).toEqual([])
    expect(await client.oauthConsents()).toEqual([])
    expect(await client.revokeOAuthConsent("client/1")).toEqual({
      revoked: true,
    })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /principals/101/tenants",
      "GET /principals/101/oauth-consents",
      "DELETE /principals/101/oauth-consents/client%2F1",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
  })

  it("normalizes nullable OAuth arrays on list, create and update responses", async () => {
    const clientData = {
      id: "client-1",
      tenant_id: "tenant-1",
      name: "Machine client",
      consent_mode: "implicit",
      redirect_uris: null,
      grant_types: null,
      scopes: null,
      public_client: false,
      status: "active",
    }
    server = Bun.serve({
      port: 0,
      fetch(request) {
        return Response.json({
          code: 0,
          data:
            request.method === "GET"
              ? [clientData]
              : request.method === "POST"
                ? { client: clientData, client_secret: "once" }
                : clientData,
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-1",
      () => "101"
    )
    const input: OAuthClientInput = {
      name: "Machine client",
      redirect_uris: [],
      grant_types: ["client_credentials"],
      scopes: ["read"],
      public_client: false,
    }
    const results = [
      (await client.oauthClients())[0],
      (await client.createOAuthClient(input)).client,
      await client.updateOAuthClient("client-1", {
        ...input,
        status: "active",
      }),
    ]
    for (const result of results) {
      expect(result?.redirect_uris).toEqual([])
      expect(result?.grant_types).toEqual([])
      expect(result?.scopes).toEqual([])
    }
  })

  it("uses the active document language for client errors after a language switch", () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, "document")
    const document = { documentElement: { lang: "zh-CN" } }
    Object.defineProperty(globalThis, "document", {
      configurable: true,
      value: document,
    })
    try {
      expect(clientErrorMessage("invalid_response")).toBe(
        "服务返回了无效响应。"
      )
      document.documentElement.lang = "ms-MY"
      expect(clientErrorMessage("invalid_response")).toBe(
        "Perkhidmatan mengembalikan respons yang tidak sah."
      )
      document.documentElement.lang = "en-US"
      expect(clientErrorMessage("invalid_response")).toBe(
        "The service returned an invalid response."
      )
    } finally {
      if (previous) Object.defineProperty(globalThis, "document", previous)
      else Reflect.deleteProperty(globalThis, "document")
    }
  })

  it("reads a tenant member authorization snapshot without losing source paths", async () => {
    const snapshot: MemberAuthorizationSnapshot = {
      subject: "member/11",
      revision: 9,
      evaluated_at: "2026-09-09T12:00:00Z",
      sources: [
        {
          type: "position",
          source_id: "position/3",
          source_name: "Finance manager",
          role_id: "role/7",
          role_name: "Reviewer",
          permission_codes: ["audit_event_view"],
          effect: "allow",
          scope_type: "tenant",
          scope_id: "tenant/1",
          data_scope: "department",
          department_ids: [],
          expires_at: "2026-10-01T00:00:00Z",
          inherited: false,
          relationship_path: [],
        },
      ],
    }
    let seenPath = ""
    let seenTenant = ""
    server = Bun.serve({
      port: 0,
      fetch(request) {
        seenPath = new URL(request.url).pathname
        seenTenant = request.headers.get("x-tenant-id") ?? ""
        return Response.json({ code: 0, message: "ok", data: snapshot })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant/1",
      () => "101"
    )

    expect(await client.memberAuthorizationSources("member/11")).toEqual(
      snapshot
    )
    expect(seenPath).toBe(
      "/tenants/tenant%2F1/members/member%2F11/authorization-sources"
    )
    expect(seenTenant).toBe("")
  })

  it("keeps archive management scoped and omits tenant headers on platform requests", async () => {
    const requests: RecordedRequest[] = []
    const completion = JSON.stringify({
      type: "complete",
      exported_events: 2,
      content_sha256: "a".repeat(64),
    })
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        if (url.pathname.endsWith("/content"))
          return new Response(completion + "\n", {
            headers: {
              "Content-Type": "application/x-ndjson",
              "Content-Disposition":
                'attachment; filename="audit-archive-123.ndjson"',
            },
          })
        return Response.json({
          code: 0,
          message: "ok",
          data: [],
          meta: { page: { total: 23 } },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-a",
      () => "101"
    )
    for (const scope of ["tenant", "platform"] as const) {
      await client.auditArchiveStatus(scope)
      expect((await client.auditArchives(20, 20, scope)).total).toBe(23)
      await client.requestAuditArchive(scope)
      await client.retryAuditArchive("123/456", scope)
      await client.verifyAuditArchive("123/456", scope)
      const download = await client.downloadAuditArchive("123", scope)
      expect(download.filename).toBe("audit-archive-123.ndjson")
      expect(await download.blob.text()).toBe(completion + "\n")
      await client.setAuditRetention(
        { min_days: 30, archive_after_days: 60 },
        scope
      )
    }
    const expected = [
      "GET /audit/archive-status",
      "GET /audit/archives?offset=20&limit=20",
      "POST /audit/archives",
      "POST /audit/archives/123%2F456/attempts",
      "POST /audit/archives/123%2F456/verifications",
      "GET /audit/archives/123/content",
      "PUT /audit/retention-policy",
    ]
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      ...expected.map((entry) => entry.replace(" /", " /tenants/tenant-a/")),
      ...expected.map((entry) => entry.replace(" /", " /platform/")),
    ])
    expect(
      requests.slice(0, 7).every(({ headers }) => !headers.has("x-tenant-id"))
    ).toBe(true)
    expect(
      requests.slice(7).every(({ headers }) => !headers.has("x-tenant-id"))
    ).toBe(true)
    expect(JSON.parse(requests[13]!.body)).toEqual({
      min_days: 30,
      archive_after_days: 60,
    })
  })

  it("rejects missing archive completion and preserves retry conflicts", async () => {
    server = Bun.serve({
      port: 0,
      fetch(request) {
        if (new URL(request.url).pathname.endsWith("/attempts"))
          return Response.json(
            { detail: "audit.archive_busy" },
            { status: 409 }
          )
        return new Response('{"type":"event"}\n', {
          headers: { "Content-Type": "application/x-ndjson" },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "11",
      () => "101"
    )
    await expect(client.downloadAuditArchive("123")).rejects.toMatchObject({
      status: 502,
      code: "incomplete_audit_export",
    })
    await expect(client.retryAuditArchive("123")).rejects.toMatchObject({
      status: 409,
      code: "audit.archive_busy",
    })
  })

  it("preserves field policy roles, tenant boundaries and optimistic versions", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: url.pathname + url.search,
          headers: request.headers,
          body: await request.text(),
        })
        if (request.method === "PUT" && requests.length === 6)
          return Response.json({ detail: "version_conflict" }, { status: 409 })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    let tenant = "tenant-a"
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => tenant,
      () => "101"
    )
    const policy = {
      read: {
        default: "deny" as const,
        allow_roles: ["9007199254740993"],
        deny_roles: ["9007199254740994"],
      },
      write: {
        default: "allow" as const,
        allow_roles: [],
        deny_roles: ["9007199254740995"],
      },
      mask: "redact" as const,
    }
    await client.fieldCatalog()
    await client.fieldPolicies("todo/a")
    await client.setFieldPolicy("todo/a", "display/name", policy, 7)
    await client.deleteFieldPolicy("todo/a", "display/name", 8)
    tenant = "tenant-b"
    await client.effectiveFields("todo", "9007199254740996")
    await expect(
      client.setFieldPolicy("todo", "title", policy, 7)
    ).rejects.toMatchObject({ status: 409 })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-a/field-catalog",
      "GET /tenants/tenant-a/field-policies?resource=todo%2Fa&offset=0&limit=200",
      "PUT /tenants/tenant-a/field-policies/todo%2Fa/display%2Fname",
      "DELETE /tenants/tenant-a/field-policies/todo%2Fa/display%2Fname?version=8",
      "GET /tenants/tenant-b/effective-fields?resource=todo&entity_id=9007199254740996",
      "PUT /tenants/tenant-b/field-policies/todo/title",
    ])
    expect(JSON.parse(requests[2]!.body)).toEqual({ policy, version: 7 })
    expect(
      requests.slice(0, 4).every(({ headers }) => !headers.has("x-tenant-id"))
    ).toBe(true)
    expect(requests[4]!.headers.get("x-tenant-id")).toBeNull()
  })

  it("uses real HTTP, cookies and the selected tenant", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-a",
      () => "101"
    )
    await client.roles()
    await client.changePassword({
      current_password: "old secure password",
      new_password: "new secure password",
    })
    expect(requests[0]?.path).toBe("/tenants/tenant-a/roles")
    expect(requests[0]?.headers.get("x-tenant-id")).toBeNull()
    expect(requests[1]?.path).toBe("/principals/101/password")
    expect(requests[1]?.method).toBe("PATCH")
    expect(requests[1]?.headers.get("x-tenant-id")).toBeNull()
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      current_password: "old secure password",
      new_password: "new secure password",
    })
  })

  it("uses tenant-scoped access governance contracts", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-governance",
      () => "101"
    )

    await client.requestableRoles()
    await client.myAccessRequests()
    await client.accessRequests()
    await client.createAccessRequest({
      role_id: "role/a",
      reason: "temporary operations",
      access_expires_at: "2026-08-04T12:00:00Z",
    })
    await client.approveAccessRequest("request/a", "approved")
    await client.rejectAccessRequest("request/b", "rejected")
    await client.revokeAccessRequest("request/c", "revoked")
    await client.withdrawAccessRequest("request/d", "withdrawn")
    await client.accessReviews()
    await client.accessReview("review/a")
    await client.createAccessReview({
      name: "Quarterly access review",
      due_at: "2026-08-10T12:00:00Z",
    })
    await client.decideAccessReviewItem("review/a", "item/a", "keep", "valid")
    await client.completeAccessReview("review/a")
    await client.cancelAccessReview("review/b")

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-governance/requestable-roles",
      "GET /tenants/tenant-governance/principals/101/access-requests",
      "GET /tenants/tenant-governance/access-requests",
      "POST /tenants/tenant-governance/access-requests",
      "PUT /access-requests/request%2Fa/decision",
      "PUT /access-requests/request%2Fb/decision",
      "PUT /access-requests/request%2Fc/grant-revocation",
      "PUT /access-requests/request%2Fd/withdrawal",
      "GET /tenants/tenant-governance/access-reviews",
      "GET /access-reviews/review%2Fa",
      "POST /tenants/tenant-governance/access-reviews",
      "PUT /access-reviews/review%2Fa/items/item%2Fa/decision",
      "PATCH /access-reviews/review%2Fa",
      "PATCH /access-reviews/review%2Fb",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[3]?.body ?? "{}")).toEqual({
      role_id: "role/a",
      reason: "temporary operations",
      access_expires_at: "2026-08-04T12:00:00Z",
    })
    expect(JSON.parse(requests[4]?.body ?? "{}")).toEqual({
      decision: "approved",
      note: "approved",
    })
    expect(JSON.parse(requests[6]?.body ?? "{}")).toEqual({
      reason: "revoked",
    })
    expect(JSON.parse(requests[10]?.body ?? "{}")).toEqual({
      name: "Quarterly access review",
      due_at: "2026-08-10T12:00:00Z",
    })
    expect(JSON.parse(requests[11]?.body ?? "{}")).toEqual({
      decision: "keep",
      note: "valid",
    })
  })

  it("maps response failures and empty success bodies", async () => {
    let count = 0
    server = Bun.serve({
      port: 0,
      fetch() {
        count++
        if (count === 1)
          return Response.json(
            { code: 403, message: "forbidden", data: null },
            { status: 403 }
          )
        return new Response(null, { status: 204 })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      undefined,
      () => "101"
    )
    await expect(client.roles()).rejects.toMatchObject({
      status: 403,
      message: "forbidden",
    })
    expect(await client.logout()).toBeUndefined()
  })

  it("preserves the localized service error and stable error code", async () => {
    server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json(
          {
            code: "last_tenant_administrator",
            message:
              "Pentadbir aktif terakhir penyewa tidak boleh dibuang atau dinyahaktifkan.",
            data: null,
          },
          {
            status: 409,
            headers: { "content-type": "application/problem+json" },
          }
        )
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      undefined,
      () => "101"
    )
    await expect(
      client.removeRoleMember("administrator", "principal")
    ).rejects.toMatchObject({
      status: 409,
      code: "last_tenant_administrator",
      message:
        "Pentadbir aktif terakhir penyewa tidak boleh dibuang atau dinyahaktifkan.",
    })
  })

  it("normalizes an empty effective menu response", async () => {
    server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json({ code: 0, message: "ok", data: null })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      undefined,
      () => "101"
    )
    expect(await client.effectiveMenus("42")).toEqual([])
  })

  it("uses public password recovery endpoints without tenant context", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({
          code: 0,
          message: "ok",
          data: requests.length === 1 ? { accepted: true } : { changed: true },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )

    expect(await client.startPasswordRecovery("operator@example.com")).toEqual({
      accepted: true,
    })
    expect(
      await client.completePasswordRecovery({
        token: "cpr1_recovery-value",
        new_password: "new secure password",
      })
    ).toEqual({ changed: true })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /password-recoveries/requests",
      "POST /password-recoveries",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[0]?.body ?? "{}")).toEqual({
      identifier: "operator@example.com",
    })
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      token: "cpr1_recovery-value",
      new_password: "new secure password",
    })
  })

  it("uses email verification endpoints without tenant context", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({
          code: 0,
          message: "ok",
          data: requests.length === 1 ? { accepted: true } : { verified: true },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )

    expect(await client.startEmailVerification()).toEqual({ accepted: true })
    expect(
      await client.completeEmailVerification("cpe1_verification-value")
    ).toEqual({ verified: true })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /email-verification-requests",
      "POST /email-verifications",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(requests[0]?.body).toBe("")
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      token: "cpe1_verification-value",
    })
  })

  it("uses public registration capabilities and registration without tenant context", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({
          code: 0,
          message: "ok",
          data:
            request.method === "GET"
              ? { registration: true, password_recovery: true, passkey: false }
              : { accepted: true },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )

    expect(await client.capabilities()).toEqual({
      registration: true,
      password_recovery: true,
      passkey: false,
    })
    expect(
      await client.register({
        email: "registered@example.com",
        password: "correct registration password",
        display_name: "Registered User",
        captcha_type: "text",
        captcha_id: "35870000000000001",
        captcha_answer: "8f2k",
      })
    ).toEqual({ accepted: true })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /login/options",
      "POST /registrations",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      email: "registered@example.com",
      password: "correct registration password",
      display_name: "Registered User",
      captcha_type: "text",
      captcha_id: "35870000000000001",
      captcha_answer: "8f2k",
    })
  })

  it("uses the public login challenge and authenticated MFA endpoints", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        const path = new URL(request.url).pathname
        if (path === "/login" && request.method === "POST")
          return Response.json({
            code: 0,
            message: "ok",
            data: {
              status: "mfa_required",
              return_url: "/",
              challenge_id: "challenge-id",
              methods: ["totp", "recovery_code"],
            },
          })
        if (path === "/principals/101/mfa")
          return Response.json({
            code: 0,
            message: "ok",
            data: { totp_enabled: true, recovery_codes_remaining: 8 },
          })
        return Response.json({
          code: 0,
          message: "ok",
          data:
            path === "/principals/101/mfa/recovery-codes" ||
            (path === "/principals/101/mfa/totp" && request.method === "POST")
              ? { recovery_codes: ["AAAA-BBBB-CCCC-DDDD"] }
              : path === "/principals/101/mfa/totp-enrollments"
                ? {
                    secret: "JBSWY3DPEHPK3PXP",
                    provisioning_uri: "otpauth://totp/IAM:user",
                    expires_at: "2026-08-01T00:10:00Z",
                  }
                : path === "/login/mfa"
                  ? { status: "authenticated", return_url: "/" }
                  : { disabled: true },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )

    expect(
      await client.login({
        login_name: "operator",
        password: "password",
        return_url: "/",
      })
    ).toMatchObject({ status: "mfa_required", challenge_id: "challenge-id" })
    expect(
      await client.verifyLoginMfa({
        challenge_id: "challenge-id",
        code: "123456",
      })
    ).toMatchObject({ status: "authenticated" })
    expect(await client.mfaStatus()).toEqual({
      totp_enabled: true,
      recovery_codes_remaining: 8,
    })
    await client.beginTotpEnrollment("password")
    await client.confirmTotpEnrollment("123456")
    await client.regenerateRecoveryCodes({
      current_password: "password",
      code: "123456",
    })
    await client.disableTotp({
      current_password: "password",
      code: "123456",
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /login",
      "POST /login/mfa",
      "GET /principals/101/mfa",
      "POST /principals/101/mfa/totp-enrollments",
      "POST /principals/101/mfa/totp",
      "PUT /principals/101/mfa/recovery-codes",
      "DELETE /principals/101/mfa/totp",
    ])
    for (const request of requests) {
      expect(request.headers.get("x-tenant-id")).toBeNull()
    }
    expect(JSON.parse(requests.at(-1)?.body ?? "{}")).toEqual({
      current_password: "password",
      code: "123456",
    })
  })

  it("uses the passkey ceremony and credential management contracts", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname
        requests.push({
          method: request.method,
          path,
          headers: request.headers,
          body: await request.text(),
        })
        const passkey = {
          id: "a".repeat(64),
          name: "Work laptop",
          sign_count: 1,
          created_at: "2026-08-01T00:00:00Z",
        }
        const data = path.endsWith("/options")
          ? {
              challenge_id: "challenge-id",
              expires_at: "2026-08-01T00:05:00Z",
              options: { publicKey: { challenge: "AQID", rpId: "localhost" } },
            }
          : path.endsWith("/verify") && path.includes("/login/")
            ? { status: "authenticated", return_url: "/" }
            : request.method === "GET"
              ? [passkey]
              : request.method === "DELETE"
                ? { deleted: true }
                : passkey
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )
    const credential = { id: "credential", type: "public-key" }

    expect(await client.passkeys()).toHaveLength(1)
    const registration = await client.beginPasskeyRegistration("password")
    await client.finishPasskeyRegistration({
      challenge_id: registration.challenge_id,
      name: "Work laptop",
      credential,
    })
    const login = await client.beginPasskeyLogin("/")
    await client.finishPasskeyLogin({
      challenge_id: login.challenge_id,
      credential,
    })
    await client.renamePasskey("a".repeat(64), "Security key")
    await client.deletePasskey("a".repeat(64), "password")

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /principals/101/passkeys",
      "POST /principals/101/passkey-registration-challenges",
      "POST /principals/101/passkeys",
      "POST /login/passkey/options",
      "POST /login/passkey/verify",
      `PATCH /principals/101/passkeys/${"a".repeat(64)}`,
      `DELETE /principals/101/passkeys/${"a".repeat(64)}`,
    ])
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      current_password: "password",
    })
    expect(JSON.parse(requests.at(-1)?.body ?? "{}")).toEqual({
      current_password: "password",
    })
  })

  it("uses the tenant-scoped OAuth client management contract", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname
        requests.push({
          method: request.method,
          path,
          headers: request.headers,
          body: await request.text(),
        })
        const client = {
          id: "client/a",
          tenant_id: "tenant-oauth",
          name: "Operations console",
          redirect_uris: ["https://console.example.test/callback"],
          grant_types: ["authorization_code", "refresh_token"],
          scopes: ["openid", "profile"],
          public_client: false,
          status: "active",
        }
        const data =
          request.method === "GET"
            ? [client]
            : path.endsWith("/credentials")
              ? { client_secret: "rotated-secret" }
              : request.method === "DELETE"
                ? { deleted: true }
                : request.method === "POST"
                  ? { client, client_secret: "created-secret" }
                  : { ...client, status: "disabled" }
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-oauth",
      () => "101"
    )
    const input: OAuthClientInput = {
      name: "Operations console",
      redirect_uris: ["https://console.example.test/callback"],
      grant_types: ["authorization_code", "refresh_token"],
      scopes: ["openid", "profile"],
      public_client: false,
    }

    expect(await client.oauthClients()).toHaveLength(1)
    expect(await client.createOAuthClient(input)).toMatchObject({
      client_secret: "created-secret",
    })
    expect(
      await client.updateOAuthClient("client/a", {
        ...input,
        grant_types: [...input.grant_types],
        status: "disabled",
      })
    ).toMatchObject({ status: "disabled" })
    expect(await client.rotateOAuthClientSecret("client/a")).toEqual({
      client_secret: "rotated-secret",
    })
    expect(await client.deleteOAuthClient("client/a")).toEqual({
      deleted: true,
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-oauth/oauth-clients",
      "POST /tenants/tenant-oauth/oauth-clients",
      "PUT /oauth-clients/client%2Fa",
      "POST /oauth-clients/client%2Fa/credentials",
      "DELETE /oauth-clients/client%2Fa",
    ])
    for (const request of requests) {
      expect(request.headers.get("x-tenant-id")).toBeNull()
    }
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual(input)
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      ...input,
      status: "disabled",
    })
  })

  it("uses the tenant-scoped SCIM directory and credential contract", async () => {
    const requests: RecordedRequest[] = []
    const directory = {
      id: "directory/a",
      tenant_id: "tenant-scim",
      name: "Workforce",
      status: "active" as const,
      version: 1,
      created_at: "2026-08-03T00:00:00Z",
      updated_at: "2026-08-03T00:00:00Z",
    }
    const credential = {
      id: "scim_credential/a",
      directory_id: directory.id,
      name: "production",
      created_at: "2026-08-03T00:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname
        requests.push({
          method: request.method,
          path,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = directory
        if (request.method === "GET" && path.endsWith("/credentials"))
          data = [credential]
        else if (request.method === "GET") data = [directory]
        else if (request.method === "POST" && path.endsWith("/credentials"))
          data = { credential, bearer_token: "scim_id.shown-once" }
        else if (request.method === "DELETE") data = { revoked: true }
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-scim",
      () => "101"
    )

    expect(await client.scimDirectories()).toEqual([directory])
    await client.createSCIMDirectory({
      name: "Workforce",
      service_account_id: "12345",
    })
    await client.updateSCIMDirectory(directory.id, {
      name: "Workforce",
      status: "disabled",
      version: 1,
      service_account_id: null,
    })
    expect(await client.scimCredentials(directory.id)).toEqual([credential])
    expect(
      await client.createSCIMCredential(directory.id, {
        name: "production",
        expires_at: "2027-08-03T00:00:00Z",
      })
    ).toMatchObject({ bearer_token: "scim_id.shown-once" })
    await client.revokeSCIMCredential(directory.id, credential.id)

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-scim/scim-directories",
      "POST /tenants/tenant-scim/scim-directories",
      "PUT /scim-directories/directory%2Fa",
      "GET /scim-directories/directory%2Fa/credentials",
      "POST /scim-directories/directory%2Fa/credentials",
      "DELETE /scim-directories/directory%2Fa/credentials/scim_credential%2Fa",
    ])
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
    expect(JSON.parse(requests[1]!.body)).toEqual({
      name: "Workforce",
      service_account_id: "12345",
    })
    expect(JSON.parse(requests[2]!.body)).toEqual({
      name: "Workforce",
      status: "disabled",
      version: 1,
      service_account_id: null,
    })
  })

  it("preserves outbound SCIM actor bindings, versions, secrets and resource operations", async () => {
    const requests: RecordedRequest[] = []
    const target = {
      id: "target/a",
      tenant_id: "tenant-outbound",
      name: "Directory",
      base_url: "https://scim.example.test/v2",
      service_account_id: "123",
      status: "active" as const,
      version: 4,
      created_at: "2026-09-06T00:00:00Z",
      updated_at: "2026-09-06T00:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname
        requests.push({
          method: request.method,
          path,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = target
        if (request.method === "GET") data = [target]
        else if (path.endsWith("/provisioning-jobs"))
          data = {
            target_id: target.id,
            resource_type: "User",
            resource_id: "456",
            external_id: "456",
            remote_id: "remote-456",
          }
        else if (path.endsWith("/deprovisioning-jobs"))
          data = { deprovisioned: true }
        else if (request.method === "DELETE") data = { deleted: true }
        else if (request.method === "POST")
          data = { target, bearer_token: "test-secret" }
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => target.tenant_id,
      () => "101"
    )
    expect(await client.scimTargets()).toEqual([target])
    const create = {
      name: target.name,
      base_url: target.base_url,
      bearer_token: "test-secret",
      service_account_id: "123",
    }
    expect(await client.createSCIMTarget(create)).toEqual({
      target,
      bearer_token: "test-secret",
    })
    const update = {
      name: "Renamed",
      base_url: target.base_url,
      status: "disabled" as const,
      version: 4,
      service_account_id: "123",
    }
    await client.updateSCIMTarget(target.id, update)
    await client.updateSCIMTarget(target.id, {
      ...update,
      version: 5,
      service_account_id: null,
      bearer_token: "new-secret",
    })
    expect(
      await client.pushSCIMResource(target.id, {
        resource_type: "User",
        resource_id: "456",
      })
    ).toMatchObject({ remote_id: "remote-456" })
    expect(
      await client.deprovisionSCIMResource(target.id, {
        resource_type: "Group",
        resource_id: "789",
      })
    ).toEqual({ deprovisioned: true })
    expect(await client.deleteSCIMTarget(target.id)).toEqual({ deleted: true })
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-outbound/scim-targets",
      "POST /tenants/tenant-outbound/scim-targets",
      "PUT /scim-targets/target%2Fa",
      "PUT /scim-targets/target%2Fa",
      "POST /scim-targets/target%2Fa/provisioning-jobs",
      "POST /scim-targets/target%2Fa/deprovisioning-jobs",
      "DELETE /scim-targets/target%2Fa",
    ])
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
    expect(JSON.parse(requests[1]!.body)).toEqual(create)
    expect(JSON.parse(requests[2]!.body)).toEqual(update)
    expect(JSON.parse(requests[2]!.body)).not.toHaveProperty("bearer_token")
    expect(JSON.parse(requests[3]!.body)).toMatchObject({
      version: 5,
      service_account_id: null,
      bearer_token: "new-secret",
    })
    expect(JSON.parse(requests[4]!.body)).toEqual({
      resource_type: "User",
      resource_id: "456",
    })
    expect(JSON.parse(requests[5]!.body)).toEqual({
      resource_type: "Group",
      resource_id: "789",
    })
  })

  it("uses the tenant-scoped service account and credential contract", async () => {
    const requests: RecordedRequest[] = []
    const account = {
      id: "service/account",
      login_name: "report-worker",
      display_name: "Report Worker",
      description: "Builds reports",
      status: "active" as const,
      version: 2,
      created_at: "2026-08-02T00:00:00Z",
      updated_at: "2026-08-02T00:00:00Z",
    }
    const credential = {
      id: "sac_credential/a",
      service_account_id: account.id,
      name: "automation",
      scopes: ["reports.read"],
      created_at: "2026-08-02T00:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = account
        if (request.method === "GET" && url.pathname.endsWith("/credentials"))
          data = [credential]
        else if (request.method === "GET") data = { items: [account], total: 1 }
        else if (
          request.method === "POST" &&
          url.pathname.endsWith("/credentials")
        )
          data = { credential, client_secret: "shown-once" }
        else if (
          request.method === "DELETE" &&
          url.pathname.includes("/credentials/")
        )
          data = { revoked: true }
        else if (request.method === "DELETE") data = { deleted: true }
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-service",
      () => "101"
    )

    expect((await client.serviceAccounts("report worker")).total).toBe(1)
    await client.createServiceAccount({
      login_name: "report-worker",
      display_name: "Report Worker",
      description: "Builds reports",
    })
    await client.updateServiceAccount(account.id, {
      display_name: "Report Worker",
      description: "Builds reports",
      status: "active",
      version: 1,
    })
    expect(await client.serviceAccountCredentials(account.id)).toEqual([
      credential,
    ])
    expect(
      await client.createServiceAccountCredential(account.id, {
        name: "automation",
        scopes: ["reports.read"],
      })
    ).toMatchObject({ client_secret: "shown-once" })
    await client.revokeServiceAccountCredential(account.id, credential.id)
    await client.deleteServiceAccount(account.id, account.version)

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-service/service-accounts?limit=200&search=report+worker",
      "POST /tenants/tenant-service/service-accounts",
      "PUT /service-accounts/service%2Faccount",
      "GET /service-accounts/service%2Faccount/credentials",
      "POST /service-accounts/service%2Faccount/credentials",
      "DELETE /service-accounts/service%2Faccount/credentials/sac_credential%2Fa",
      "DELETE /service-accounts/service%2Faccount?version=2",
    ])
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
  })

  it("uses the tenant department hierarchy contract", async () => {
    const requests: RecordedRequest[] = []
    const department = {
      id: "department/a",
      tenant_id: "tenant-organization",
      entity_id: "entity/a",
      name: "Engineering",
      status: "active" as const,
      sort_order: 10,
      depth: 0,
      version: 1,
      created_at: "2026-08-02T00:00:00Z",
      updated_at: "2026-08-02T00:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        const data =
          request.method === "DELETE"
            ? { deleted: true }
            : request.method === "GET" &&
                url.pathname === "/tenants/tenant-organization/departments"
              ? [department]
              : request.method === "PATCH"
                ? { ...department, version: 2, name: "Platform" }
                : department
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-organization",
      () => "101"
    )
    const input: DepartmentInput = {
      entity_id: "entity/a",
      parent_id: "",
      name: "Engineering",
      status: "active",
      sort_order: 10,
    }

    expect(await client.departments()).toEqual([department])
    expect(await client.department("department/a")).toEqual(department)
    expect(await client.createDepartment(input)).toEqual(department)
    expect(
      await client.updateDepartment("department/a", {
        parent_id: input.parent_id,
        name: "Platform",
        status: input.status,
        sort_order: input.sort_order,
        version: 1,
      })
    ).toMatchObject({ name: "Platform", version: 2 })
    expect(await client.deleteDepartment("department/a", 2)).toEqual({
      deleted: true,
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-organization/departments?offset=0&limit=200",
      "GET /departments/department%2Fa",
      "POST /tenants/tenant-organization/departments",
      "PATCH /departments/department%2Fa",
      "DELETE /departments/department%2Fa?version=2",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual(input)
    expect(JSON.parse(requests[3]?.body ?? "{}")).toEqual({
      parent_id: input.parent_id,
      name: "Platform",
      status: input.status,
      sort_order: input.sort_order,
      version: 1,
    })
  })

  it("uses encoded tenant administration mutation contracts", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-admin",
      () => "101"
    )

    await client.updatePrincipal("principal/a", {
      display_name: "Alice",
      email: "alice@example.test",
    })
    await client.updateMember("principal/a", {
      status: "disabled",
      department_id: "department/a",
    })
    await client.updateRole("role/a", { name: "Operators" })
    await client.roleMembers("role/a")
    await client.addRoleMember("role/a", "principal/a")
    await client.removeRoleMember("role/a", "principal/a")
    await client.roleDirectoryBindings("role/a")
    await client.roleDataScope("role/a")
    await client.setRoleDataScope("role/a", {
      scope: "selected_departments",
      department_ids: ["department/a"],
    })
    await client.addRoleDirectoryBinding("role/a", "group", "group/a")
    await client.removeRoleDirectoryBinding("role/a", "position", "position/a")
    await client.rolePermissionGrants("role/a")
    await client.grantPermission("role/a", "user:view")
    await client.setRolePermissionCondition("role/a", "user:view", {
      version: 1,
      gte: [{ context: "auth.acr" }, { value: 2 }],
    })
    await client.clearRolePermissionCondition("role/a", "user:view")
    await client.revokePermission("role/a", "user:view")
    await client.updateMenu("menu/a", {
      label: "Operators",
      status: "disabled",
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "PATCH /tenants/tenant-admin/principals/principal%2Fa",
      "PATCH /tenants/tenant-admin/members/principal%2Fa",
      "PATCH /roles/role%2Fa",
      "GET /roles/role%2Fa/members",
      "PUT /roles/role%2Fa/members/principal%2Fa",
      "DELETE /roles/role%2Fa/members/principal%2Fa",
      "GET /roles/role%2Fa/directory-bindings",
      "GET /roles/role%2Fa/data-scope",
      "PUT /roles/role%2Fa/data-scope",
      "PUT /roles/role%2Fa/directory-bindings/group/group%2Fa",
      "DELETE /roles/role%2Fa/directory-bindings/position/position%2Fa",
      "GET /roles/role%2Fa/permission-grants",
      "PUT /roles/role%2Fa/permissions/user%3Aview",
      "PUT /roles/role%2Fa/permissions/user%3Aview/condition",
      "DELETE /roles/role%2Fa/permissions/user%3Aview/condition",
      "DELETE /roles/role%2Fa/permissions/user%3Aview",
      "PATCH /menus/menu%2Fa",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[0]?.body ?? "{}")).toEqual({
      display_name: "Alice",
      email: "alice@example.test",
    })
    expect(JSON.parse(requests[13]?.body ?? "{}")).toEqual({
      condition: {
        version: 1,
        gte: [{ context: "auth.acr" }, { value: 2 }],
      },
    })
    expect(JSON.parse(requests.at(-1)?.body ?? "{}")).toEqual({
      label: "Operators",
      status: "disabled",
    })
  })

  it("uses tenant-scoped invitation management and public acceptance", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-invitation",
      () => "101"
    )

    await client.invitations()
    await client.createInvitation({
      email: "user@example.test",
      department_id: "department/a",
      role_ids: ["role/a"],
      expires_in_hours: 48,
    })
    await client.resendInvitation("invitation/a", 24)
    await client.revokeInvitation("invitation/a")
    await client.acceptInvitation({
      token: "inv1_invitation/a.secret",
      login_name: "invited",
      password: "correct horse battery staple",
      display_name: "Invited User",
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-invitation/invitations",
      "POST /tenants/tenant-invitation/invitations",
      "POST /invitations/invitation%2Fa/deliveries",
      "DELETE /invitations/invitation%2Fa",
      "POST /invitation-acceptances",
    ])
    expect(
      requests.slice(0, 4).every(({ headers }) => !headers.has("x-tenant-id"))
    ).toBe(true)
    expect(requests[4]?.headers.get("x-tenant-id")).toBeNull()
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      expires_in_hours: 24,
    })
  })

  it("uses the platform tenant lifecycle contract without tenant context", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-must-not-leak",
      () => "101"
    )

    await client.tenants(true)
    await client.createTenant({
      slug: "acme",
      name: "Acme",
      owner_principal_id: "principal-1",
    })
    await client.updateTenant("tenant/a", {
      name: "Acme Global",
      status: "suspended",
      version: 2,
    })
    await client.deleteTenant("tenant/a", 3)

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants?include_deleted=true&offset=0&limit=200",
      "POST /tenants",
      "PATCH /tenants/tenant%2Fa",
      "DELETE /tenants/tenant%2Fa?version=3",
    ])
    expect(
      requests.every(({ headers }) => headers.get("x-tenant-id") === null)
    ).toBe(true)
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      slug: "acme",
      name: "Acme",
      owner_principal_id: "principal-1",
    })
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      name: "Acme Global",
      status: "suspended",
      version: 2,
    })
  })

  it("uses the published PUT for platform principal anonymization", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: {} })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-a",
      () => "101"
    )
    await client.anonymizePrincipal("principal/a", {
      version: 3,
      confirmation: "confirmed",
    })
    expect(requests[0]?.method).toBe("PUT")
    expect(requests[0]?.path).toBe(
      "/platform/principals/principal%2Fa/lifecycle/anonymization"
    )
    expect(JSON.parse(requests[0]?.body ?? "{}")).toEqual({
      version: 3,
      confirmation: "confirmed",
    })
    expect(requests[0]?.headers.has("x-tenant-id")).toBe(false)
  })

  it("uses the complete position and position-member contract over real HTTP", async () => {
    const requests: RecordedRequest[] = []
    const position = {
      id: "position/a",
      tenant_id: "tenant-positions",
      code: "platform.engineer",
      name: "Platform Engineer",
      status: "active" as const,
      sort_order: 10,
      version: 7,
      created_at: "2026-08-02T12:00:00Z",
      updated_at: "2026-08-02T12:00:00Z",
    }
    const member = {
      position_id: position.id,
      principal_id: "principal/a",
      display_name: "Alice",
      starts_at: "2026-08-03T00:00:00Z",
      ends_at: "2026-09-03T00:00:00Z",
      created_at: "2026-08-02T12:00:00Z",
      updated_at: "2026-08-02T12:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = position
        if (request.method === "DELETE") data = { deleted: true }
        else if (url.pathname.endsWith("/members")) data = [member]
        else if (url.pathname.includes("/members/")) data = member
        else if (
          request.method === "GET" &&
          url.pathname === "/tenants/tenant-positions/positions"
        )
          data = [position]
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-positions",
      () => "101"
    )

    expect(await client.positions()).toEqual([position])
    expect(await client.position(position.id)).toEqual(position)
    await client.createPosition({
      code: position.code,
      name: position.name,
      status: position.status,
      sort_order: position.sort_order,
    })
    await client.updatePosition(position.id, {
      name: "Principal Platform Engineer",
      version: position.version,
    })
    expect(await client.deletePosition(position.id, position.version)).toEqual({
      deleted: true,
    })
    expect(await client.positionMembers(position.id)).toEqual([member])
    expect(
      await client.putPositionMember(position.id, member.principal_id, {
        starts_at: member.starts_at,
        ends_at: member.ends_at,
      })
    ).toEqual(member)
    expect(
      await client.deletePositionMember(position.id, member.principal_id)
    ).toEqual({ deleted: true })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-positions/positions?offset=0&limit=200",
      "GET /positions/position%2Fa",
      "POST /tenants/tenant-positions/positions",
      "PATCH /positions/position%2Fa",
      "DELETE /positions/position%2Fa?version=7",
      "GET /positions/position%2Fa/members?offset=0&limit=200",
      "PUT /positions/position%2Fa/members/principal%2Fa",
      "DELETE /positions/position%2Fa/members/principal%2Fa",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      code: position.code,
      name: position.name,
      status: position.status,
      sort_order: position.sort_order,
    })
    expect(JSON.parse(requests[3]?.body ?? "{}")).toEqual({
      name: "Principal Platform Engineer",
      version: position.version,
    })
    expect(JSON.parse(requests[6]?.body ?? "{}")).toEqual({
      starts_at: member.starts_at,
      ends_at: member.ends_at,
    })
  })

  it("uses the complete group and group-member contract over real HTTP", async () => {
    const requests: RecordedRequest[] = []
    const group = {
      id: "group/a",
      tenant_id: "tenant-groups",
      name: "Platform Operators",
      type: "dynamic" as const,
      membership_rule: {
        version: 1 as const,
        match: "all" as const,
        conditions: [
          {
            field: "member.email_domain" as const,
            operator: "in" as const,
            values: ["example.com"],
          },
        ],
      },
      description: "Production platform operators",
      status: "active" as const,
      sort_order: 10,
      version: 7,
      created_at: "2026-08-02T12:00:00Z",
      updated_at: "2026-08-02T12:00:00Z",
    }
    const member = {
      group_id: group.id,
      principal_id: "principal/a",
      display_name: "Alice",
      starts_at: "2026-08-03T00:00:00Z",
      ends_at: "2026-09-03T00:00:00Z",
      created_at: "2026-08-02T12:00:00Z",
      updated_at: "2026-08-02T12:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = group
        if (request.method === "DELETE") data = { deleted: true }
        else if (url.pathname.endsWith("/members")) data = [member]
        else if (url.pathname.includes("/members/")) data = member
        else if (
          request.method === "GET" &&
          url.pathname === "/tenants/tenant-groups/groups"
        )
          data = [group]
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-groups",
      () => "101"
    )

    expect(await client.groups()).toEqual([group])
    expect(await client.group(group.id)).toEqual(group)
    await client.createGroup({
      name: group.name,
      type: group.type,
      membership_rule: group.membership_rule,
      description: group.description,
      status: group.status,
      sort_order: group.sort_order,
    })
    await client.updateGroup(group.id, {
      name: "Security Operators",
      membership_rule: group.membership_rule,
      version: group.version,
    })
    expect(await client.deleteGroup(group.id, group.version)).toEqual({
      deleted: true,
    })
    expect(await client.groupMembers(group.id)).toEqual([member])
    expect(
      await client.putGroupMember(group.id, member.principal_id, {
        starts_at: member.starts_at,
        ends_at: member.ends_at,
      })
    ).toEqual(member)
    expect(
      await client.deleteGroupMember(group.id, member.principal_id)
    ).toEqual({ deleted: true })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-groups/groups?offset=0&limit=200",
      "GET /groups/group%2Fa",
      "POST /tenants/tenant-groups/groups",
      "PATCH /groups/group%2Fa",
      "DELETE /groups/group%2Fa?version=7",
      "GET /groups/group%2Fa/members?offset=0&limit=200",
      "PUT /groups/group%2Fa/members/principal%2Fa",
      "DELETE /groups/group%2Fa/members/principal%2Fa",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      name: group.name,
      type: group.type,
      membership_rule: group.membership_rule,
      description: group.description,
      status: group.status,
      sort_order: group.sort_order,
    })
    expect(JSON.parse(requests[3]?.body ?? "{}")).toEqual({
      name: "Security Operators",
      membership_rule: group.membership_rule,
      version: group.version,
    })
    expect(JSON.parse(requests[6]?.body ?? "{}")).toEqual({
      starts_at: member.starts_at,
      ends_at: member.ends_at,
    })
  })

  it("uses the complete entity and scoped role-binding contract over real HTTP", async () => {
    const requests: RecordedRequest[] = []
    const entity = {
      id: "entity/a",
      tenant_id: "tenant-entities",
      parent_id: "entity/root",
      type: "store",
      name: "Flagship",
      status: "active" as const,
      metadata: { region: "west" },
      created_at: "2026-08-02T12:00:00Z",
      updated_at: "2026-08-02T12:00:00Z",
    }
    const binding = {
      entity_id: entity.id,
      role_id: "role/a",
      principal_id: "principal/a",
      effect: "deny" as const,
      expires_at: "2026-09-03T00:00:00Z",
      created_at: "2026-08-02T12:00:00Z",
    }
    const relationshipInput = {
      subject_type: "principal" as const,
      subject_id: binding.principal_id,
      relation: "viewer" as const,
      resource_type: entity.type,
      resource_id: entity.id,
      starts_at: "2026-08-03T00:00:00Z",
      ends_at: "2026-09-03T00:00:00Z",
      condition: {
        version: 1 as const,
        all: [
          {
            gte: [{ context: "auth.acr" as const }, { value: 1 }] as [
              { context: "auth.acr" },
              { value: number },
            ],
          },
        ],
      },
    }
    const relationship = {
      ...relationshipInput,
      created_at: "2026-08-02T12:00:00Z",
    }
    const resourceRelationshipInput = {
      ...relationshipInput,
      entity_id: entity.id,
      resource_id: "business/store/a",
    }
    const resourceRelationship = {
      ...resourceRelationshipInput,
      created_at: relationship.created_at,
    }
    const constraint = {
      allow_all: true,
      owner_ids: [],
      resource_ids: [],
      department_ids: [],
      ancestors: [{ type: "entity", id: entity.parent_id }],
      denied_ids: [entity.id],
      revision: 7,
    }
    const explanation = {
      allowed: false,
      reason: "explicit_deny" as const,
      revision: 7,
      matches: [
        {
          permission_code: "store_view",
          role_id: binding.role_id,
          source_type: "entity_binding",
          source_id: entity.id,
          scope_type: "entity",
          scope_id: entity.id,
          effect: "deny" as const,
          inherited: false,
        },
      ],
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        let data: unknown = entity
        if (
          url.pathname ===
          "/tenants/tenant-entities/authorization-constraint-evaluations"
        )
          data = constraint
        else if (
          url.pathname === "/tenants/tenant-entities/authorization-explanations"
        )
          data = explanation
        else if (
          url.pathname === "/tenants/tenant-entities/authorization-checks"
        )
          data = {
            allowed: true,
            reason: "relationship_grant",
            revision: 8,
          }
        else if (
          request.method === "GET" &&
          url.pathname === "/tenants/tenant-entities/relationships"
        )
          data = url.searchParams.has("entity_id")
            ? [resourceRelationship]
            : [relationship]
        else if (
          request.method === "POST" &&
          url.pathname === "/tenants/tenant-entities/relationships"
        )
          data = relationship
        else if (request.method === "DELETE")
          data = { changed: true, sync_status: "applied" }
        else if (url.pathname.endsWith("/role-bindings")) data = [binding]
        else if (url.pathname.includes("/role-bindings/")) data = binding
        else if (
          request.method === "GET" &&
          url.pathname === "/tenants/tenant-entities/entities"
        )
          data = [entity]
        return Response.json({ code: 0, message: "ok", data })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-entities",
      () => "101"
    )

    expect(await client.entities()).toEqual([entity])
    expect(await client.entity(entity.id)).toEqual(entity)
    await client.createEntity({
      parent_id: entity.parent_id,
      type: entity.type,
      name: entity.name,
      status: entity.status,
      metadata: entity.metadata,
    })
    await client.updateEntity(entity.id, { name: "Downtown" })
    expect(await client.entityRoleBindings(entity.id)).toEqual([binding])
    expect(
      await client.putEntityRoleBinding(
        entity.id,
        binding.role_id,
        binding.principal_id,
        { effect: binding.effect, expires_at: binding.expires_at }
      )
    ).toEqual(binding)
    expect(
      await client.deleteEntityRoleBinding(
        entity.id,
        binding.role_id,
        binding.principal_id
      )
    ).toEqual({ changed: true, sync_status: "applied" })
    expect(await client.relationships(entity.id)).toEqual([relationship])
    expect(await client.putRelationship(relationshipInput)).toEqual(
      relationship
    )
    expect(JSON.parse(requests.at(-1)?.body ?? "{}")).toEqual(relationshipInput)
    expect(await client.deleteRelationship(relationshipInput)).toEqual({
      changed: true,
      sync_status: "applied",
    })
    expect(await client.resourceRelationships(entity.id)).toEqual([
      resourceRelationship,
    ])
    expect(await client.deleteRelationship(resourceRelationshipInput)).toEqual({
      changed: true,
      sync_status: "applied",
    })
    expect(
      await client.checkEntityAuthorization(
        entity.id,
        "store_view",
        binding.principal_id
      )
    ).toEqual({ allowed: true, reason: "relationship_grant", revision: 8 })
    expect(
      await client.checkResourceAuthorization(
        entity.id,
        "store",
        "business/store/a",
        "store_view",
        binding.principal_id
      )
    ).toEqual({ allowed: true, reason: "relationship_grant", revision: 8 })
    expect(
      await client.authorizationConstraint("store_view", binding.principal_id)
    ).toEqual(constraint)
    expect(
      await client.explainEntityAuthorization(
        entity.id,
        "store_view",
        binding.principal_id
      )
    ).toEqual(explanation)
    expect(
      await client.explainResourceAuthorization(
        entity.id,
        "store",
        "business/store/a",
        "store_view",
        binding.principal_id
      )
    ).toEqual(explanation)
    expect(await client.deleteEntity(entity.id)).toEqual({
      changed: true,
      sync_status: "applied",
    })

    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-entities/entities",
      "GET /entities/entity%2Fa",
      "POST /tenants/tenant-entities/entities",
      "PATCH /entities/entity%2Fa",
      "GET /entities/entity%2Fa/role-bindings?offset=0&limit=200",
      "PUT /entities/entity%2Fa/role-bindings/role%2Fa/principal%2Fa",
      "DELETE /entities/entity%2Fa/role-bindings/role%2Fa/principal%2Fa",
      "GET /tenants/tenant-entities/relationships?resource_id=entity%2Fa&offset=0&limit=200",
      "POST /tenants/tenant-entities/relationships",
      "DELETE /tenants/tenant-entities/relationships?subject_type=principal&subject_id=principal%2Fa&relation=viewer&resource_type=store&resource_id=entity%2Fa",
      "GET /tenants/tenant-entities/relationships?entity_id=entity%2Fa",
      "DELETE /tenants/tenant-entities/relationships?entity_id=entity%2Fa&subject_type=principal&subject_id=principal%2Fa&relation=viewer&resource_type=store&resource_id=business%2Fstore%2Fa",
      "POST /tenants/tenant-entities/authorization-checks",
      "POST /tenants/tenant-entities/authorization-checks",
      "POST /tenants/tenant-entities/authorization-constraint-evaluations",
      "POST /tenants/tenant-entities/authorization-explanations",
      "POST /tenants/tenant-entities/authorization-explanations",
      "DELETE /entities/entity%2Fa",
    ])
    expect(requests.every(({ headers }) => !headers.has("x-tenant-id"))).toBe(
      true
    )
    expect(JSON.parse(requests[2]?.body ?? "{}")).toEqual({
      parent_id: entity.parent_id,
      type: entity.type,
      name: entity.name,
      status: entity.status,
      metadata: entity.metadata,
    })
    expect(JSON.parse(requests[5]?.body ?? "{}")).toEqual({
      effect: binding.effect,
      expires_at: binding.expires_at,
    })
    expect(JSON.parse(requests[8]?.body ?? "{}")).toEqual(relationshipInput)
    expect(JSON.parse(requests[12]?.body ?? "{}")).toEqual({
      entity_id: entity.id,
      permission_code: "store_view",
      subject: binding.principal_id,
    })
    expect(JSON.parse(requests[13]?.body ?? "{}")).toEqual({
      entity_id: entity.id,
      resource_type: "store",
      resource_id: "business/store/a",
      permission_code: "store_view",
      subject: binding.principal_id,
    })
    expect(JSON.parse(requests[14]?.body ?? "{}")).toEqual({
      permission_code: "store_view",
      subject: binding.principal_id,
    })
    expect(JSON.parse(requests[15]?.body ?? "{}")).toEqual({
      entity_id: entity.id,
      permission_code: "store_view",
      subject: binding.principal_id,
    })
    expect(JSON.parse(requests[16]?.body ?? "{}")).toEqual({
      entity_id: entity.id,
      resource_type: "store",
      resource_id: "business/store/a",
      permission_code: "store_view",
      subject: binding.principal_id,
    })
  })

  it("reads tenant audit pagination, detail and chain integrity", async () => {
    const requests: RecordedRequest[] = []
    const event: AuditEvent = {
      id: "event-1",
      tenant_id: "tenant-audit",
      event_type: "oauth_client_created",
      target_type: "oauth_client",
      target_id: "client-1",
      outcome: "success",
      detail: { public_client: false },
      sequence: 1,
      event_hash: "a".repeat(64),
      created_at: "2026-08-01T15:00:00Z",
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        const url = new URL(request.url)
        requests.push({
          method: request.method,
          path: `${url.pathname}${url.search}`,
          headers: request.headers,
          body: await request.text(),
        })
        if (
          url.pathname === "/tenants/tenant-audit/audit/integrity-verifications"
        )
          return Response.json({
            code: 0,
            message: "ok",
            data: {
              tenant_id: "tenant-audit",
              valid: true,
              verified_events: 1,
              head_sequence: 1,
              head_hash: "a".repeat(64),
            },
          })
        if (url.pathname === "/tenants/tenant-audit/audit/exports")
          return new Response(
            [
              JSON.stringify({
                type: "manifest",
                schema: "audit-export.v1",
                tenant_id: "tenant-audit",
                head_sequence: 2,
              }),
              JSON.stringify({ type: "event", event }),
              JSON.stringify({
                type: "complete",
                exported_events: 1,
                content_sha256: "b".repeat(64),
              }),
              "",
            ].join("\n"),
            {
              headers: {
                "Content-Type": "application/x-ndjson",
                "Content-Disposition":
                  'attachment; filename="audit-20260801T150000Z.ndjson"',
              },
            }
          )
        if (url.pathname === "/tenants/tenant-audit/audit/events/event-1")
          return Response.json({ code: 0, message: "ok", data: event })
        return Response.json({
          code: 0,
          message: "ok",
          data: [event],
          meta: { page: { offset: 0, limit: 50, count: 1, total: 7 } },
        })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-audit",
      () => "101"
    )

    expect(
      await client.auditEvents({ event_type: "oauth_client_created" })
    ).toEqual({
      items: [event],
      total: 7,
    })
    expect(await client.auditEvent("event-1")).toEqual(event)
    expect(await client.auditIntegrity()).toMatchObject({
      valid: true,
      head_sequence: 1,
    })
    const download = await client.auditExport({
      event_type: "oauth_client_created",
    })
    expect(download.filename).toBe("audit-20260801T150000Z.ndjson")
    expect(await download.blob.text()).toContain('"schema":"audit-export.v1"')
    expect(requests.map(({ path }) => path)).toEqual([
      "/tenants/tenant-audit/audit/events?event_type=oauth_client_created",
      "/tenants/tenant-audit/audit/events/event-1",
      "/tenants/tenant-audit/audit/integrity-verifications",
      "/tenants/tenant-audit/audit/exports?event_type=oauth_client_created",
    ])
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
  })

  it("rejects invalid or incomplete audit export responses", async () => {
    let requestCount = 0
    server = Bun.serve({
      port: 0,
      fetch() {
        requestCount++
        if (requestCount === 1)
          return new Response("not ndjson", {
            headers: { "Content-Type": "text/plain" },
          })
        return new Response(
          '{"type":"manifest","schema":"audit-export.v1"}\n',
          { headers: { "Content-Type": "application/x-ndjson" } }
        )
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      undefined,
      () => "101"
    )
    await expect(client.auditExport()).rejects.toMatchObject({
      status: 502,
      message: "The audit export returned an unexpected file type.",
    })
    await expect(client.auditExport()).rejects.toMatchObject({
      status: 502,
      message: "The audit export is incomplete.",
    })
  })

  it("uses real HTTP for WORM audit governance contracts", async () => {
    const requests: RecordedRequest[] = []
    const governance = {
      code: 0,
      message: "ok",
      data: {
        policy: {
          tenant_id: "tenant-audit",
          min_days: 365,
          archive_after_days: 730,
        },
        integrity: {
          tenant_id: "tenant-audit",
          valid: true,
          verified_events: 7,
          head_sequence: 7,
          anchor: { enabled: true, sequence: 7, signed: true, valid: true },
        },
        anchors: [
          {
            schema: "audit-anchor.v1",
            tenant_id: "tenant-audit",
            head_sequence: 7,
            head_hash: "abcd",
            anchored_at: "2026-08-06T00:00:00Z",
            anchor_hash: "ef01",
            signing_key_id: "key-1",
            root_public_key: "pub",
            root_signature: "sig",
          },
        ],
        total_events: 42,
        archive_ready_events: 3,
        anchored_events: 7,
      },
    }
    const retention = {
      code: 0,
      message: "ok",
      data: {
        tenant_id: "tenant-audit",
        min_days: 180,
        archive_after_days: 360,
      },
    }
    const anchor = {
      code: 0,
      message: "ok",
      data: governance.data.anchors[0],
    }
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        if (request.method === "GET") return Response.json(governance)
        if (request.method === "PUT") return Response.json(retention)
        return Response.json(anchor)
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "tenant-audit",
      () => "101"
    )
    const report = await client.auditGovernance()
    expect(report.policy.min_days).toBe(365)
    expect(report.integrity.anchor?.signed).toBe(true)
    expect(report.anchors[0]?.root_signature).toBe("sig")
    const updated = await client.setAuditRetention({
      min_days: 180,
      archive_after_days: 360,
    })
    expect(updated.archive_after_days).toBe(360)
    const signed = await client.auditSignRoot()
    expect(signed.signing_key_id).toBe("key-1")
    expect(requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "GET /tenants/tenant-audit/audit/governance-policy",
      "PUT /tenants/tenant-audit/audit/retention-policy",
      "POST /tenants/tenant-audit/audit/root-signatures",
    ])
    expect(JSON.parse(requests[1]?.body ?? "{}")).toEqual({
      min_days: 180,
      archive_after_days: 360,
    })
    for (const request of requests)
      expect(request.headers.get("x-tenant-id")).toBeNull()
  })

  it("omits the tenant header while no tenant is resolved", async () => {
    const requests: RecordedRequest[] = []
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        requests.push({
          method: request.method,
          path: new URL(request.url).pathname,
          headers: request.headers,
          body: await request.text(),
        })
        return Response.json({ code: 0, message: "ok", data: [] })
      },
    })
    const client = createIamApi(
      `http://127.0.0.1:${server.port}`,
      () => "",
      () => "101"
    )
    await client.roles()
    // Sending an empty header would scope the request to a tenant named "",
    // which reads as a real choice to the service; leaving it off does not.
    expect(requests[0]?.headers.has("x-tenant-id")).toBe(false)
  })
})
