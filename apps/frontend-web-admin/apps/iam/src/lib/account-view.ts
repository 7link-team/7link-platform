import { apiRoute } from "./api-route"

export type AccountViewScope =
  "system" | "platform" | "tenant" | "entity" | "personal"
export type AccountViewMode = "read_only" | "read_write"

export interface AccountViewSelection {
  principal_id?: string
  target_type?: "account" | "platform" | "tenant"
  scope_type: AccountViewScope
  scope_id: string
  mode: AccountViewMode
}

export interface ActiveAccountView extends AccountViewSelection {
  id: string
  expires_at: string
  returnPath: string
}

let current: ActiveAccountView | null = null
let secret = ""
let revision = 0
let tenant = ""
const tabMarker = "iam.account-view-tab"
const handoffPath = "/account-view/open"
const windowPrefix = "iam-account-view:"
let isolatedTab =
  typeof window !== "undefined" &&
  (window.location.pathname === handoffPath ||
    window.name?.startsWith(windowPrefix))
try {
  isolatedTab ||=
    typeof sessionStorage !== "undefined" &&
    sessionStorage.getItem(tabMarker) === "1"
} catch {
  isolatedTab = true
}
const listeners = new Set<() => void>()

function clearAccountView() {
  current = null
  secret = ""
  tenant = ""
  revision++
  window.dispatchEvent(new Event("account-view-ended"))
  listeners.forEach((listener) => listener())
}

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => {
    if (isolatedTab) clearAccountView()
  })
  window.addEventListener("pageshow", (event) => {
    if (isolatedTab && event.persisted) clearAccountView()
  })
}

export const getAccountView = () => current
export const getAccountViewRevision = () => revision
export const getViewTenant = () => tenant
export function setViewTenant(value: string) {
  tenant = value
}
export function subscribeAccountView(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function activateAccountView(
  selection: AccountViewSelection,
  issued: {
    id: string
    token: string
    expires_at: string
    mode: AccountViewMode
  },
  returnPath: string,
  tenantID: string
) {
  if (current) throw new Error("An account view is already open")
  if (
    !issued.token.startsWith("avt_") ||
    !Number.isFinite(Date.parse(issued.expires_at))
  )
    throw new Error("Invalid account view response")
  secret = issued.token
  isolatedTab = true
  tenant = tenantID
  current = {
    ...selection,
    id: issued.id,
    expires_at: issued.expires_at,
    mode: issued.mode,
    returnPath,
  }
  revision++
  listeners.forEach((listener) => listener())
}

export function accountViewRequest(
  method: string
): Pick<RequestInit, "credentials" | "headers"> {
  if (!current || !secret) {
    if (isolatedTab) throw new Error("This account view is closed")
    return { credentials: "include" }
  }
  if (Date.parse(current.expires_at) <= Date.now()) {
    window.dispatchEvent(new Event("account-view-expired"))
    throw new Error("Account view expired")
  }
  if (
    current.mode === "read_only" &&
    !["GET", "HEAD", "OPTIONS"].includes(method.toUpperCase())
  )
    throw new Error("This account view is read-only")
  return { credentials: "omit", headers: { Authorization: `Bearer ${secret}` } }
}

export function accountViewResponse(status: number, requestRevision: number) {
  if (current && revision === requestRevision && status === 401)
    window.dispatchEvent(new Event("account-view-expired"))
}

export async function exitAccountView() {
  const view = current
  if (!view) return
  // Cleared first, and the revocation is best effort, for the same reason as
  // sign-out: leaving the view must not depend on the service answering.
  clearAccountView()
  try {
    await revokeAccountView(view.id)
  } catch {
    // Deliberately ignored: the view is already gone from this tab, and it
    // carries an expiry the service enforces without being asked.
  }
  window.close()
}

async function revokeAccountView(id: string) {
  const route = apiRoute("delete", "/account-views/{account_view_id}", {
    path: { account_view_id: id },
  })
  const response = await fetch(
    `${(import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")}${route.url}`,
    {
      method: route.method,
      credentials: "include",
      headers: { Origin: window.location.origin },
    }
  )
  if (!response.ok && response.status !== 401 && response.status !== 404)
    throw new Error("Unable to close account view")
}

export async function issueAccountView(selection: AccountViewSelection) {
  if (current || isolatedTab) throw new Error("An account view is already open")
  const route = apiRoute("post", "/account-views")
  const response = await fetch(
    `${(import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")}${route.url}`,
    {
      method: route.method,
      credentials: "include",
      headers: {
        "Content-Type": "application/json",
        Origin: window.location.origin,
      },
      body: JSON.stringify(selection),
    }
  )
  if (!response.ok) throw new Error("Unable to open account view")
  const body = (await response.json()) as {
    data: {
      id: string
      token: string
      expires_at: string
      mode: AccountViewMode
    }
  }
  return body.data
}

export function accountViewPath(
  selection: AccountViewSelection,
  tenantID: string
) {
  const id = encodeURIComponent(selection.scope_id)
  switch (selection.scope_type) {
    case "system":
      return "/system"
    case "platform":
      return `/platform/${id}`
    case "tenant":
      return `/admin/${id}`
    case "entity":
      return `/admin/${encodeURIComponent(tenantID)}/entities/${id}/overview`
    case "personal":
      return "/user/profile"
  }
}

export async function openAccountViewTab(
  selection: AccountViewSelection,
  returnPath: string,
  tenantID: string
) {
  const nonce = crypto.randomUUID()
  let popup: Window | null = null
  let issued: Awaited<ReturnType<typeof issueAccountView>> | undefined
  let transferred = false
  let ready: () => void = () => {}
  let accepted: () => void = () => {}
  let rejectReady: (error: Error) => void = () => {}
  let rejectAccepted: (error: Error) => void = () => {}
  const readyPromise = new Promise<void>((resolve, reject) => {
    ready = resolve
    rejectReady = reject
  })
  const acceptedPromise = new Promise<void>((resolve, reject) => {
    accepted = resolve
    rejectAccepted = reject
  })
  // Both promises can fail while issuance is pending; attach handlers immediately.
  void readyPromise.catch(() => {})
  void acceptedPromise.catch(() => {})
  const receive = (event: MessageEvent) => {
    if (
      event.origin !== window.location.origin ||
      event.source !== popup ||
      event.data?.nonce !== nonce
    )
      return
    if (event.data.type === "account-view-ready") ready()
    if (transferred && event.data.type === "account-view-accepted") accepted()
  }
  const fail = () => {
    const error = new Error("Unable to open account view tab")
    rejectReady(error)
    rejectAccepted(error)
  }
  window.addEventListener("message", receive)
  const timeout = window.setTimeout(fail, 30_000)
  const closed = window.setInterval(() => {
    if (popup?.closed) fail()
  }, 250)
  try {
    popup = window.open(handoffPath, `${windowPrefix}${nonce}`)
    if (!popup) throw new Error("Unable to open account view tab")
    await readyPromise
    issued = await issueAccountView(selection)
    if (popup.closed) throw new Error("Account view tab was closed")
    transferred = true
    popup.postMessage(
      {
        type: "account-view-activate",
        nonce,
        selection,
        issued,
        returnPath,
        tenantID,
      },
      window.location.origin
    )
    await acceptedPromise
  } catch (cause) {
    popup?.close()
    if (issued) await revokeAccountView(issued.id).catch(() => {})
    throw cause
  } finally {
    if (issued) issued.token = ""
    window.removeEventListener("message", receive)
    window.clearTimeout(timeout)
    window.clearInterval(closed)
  }
}

export async function initializeAccountViewTab(): Promise<
  "normal" | "active" | "closed"
> {
  if (!isolatedTab) return "normal"
  try {
    sessionStorage.setItem(tabMarker, "1")
  } catch {
    return "closed"
  }
  if (window.location.pathname !== handoffPath) return "closed"
  const opener: Window | null = window.opener
  const nonce = window.name.startsWith(windowPrefix)
    ? window.name.slice(windowPrefix.length)
    : ""
  if (!opener || !nonce || nonce === "closed") return "closed"
  return new Promise((resolve) => {
    const finish = (result: "active" | "closed") => {
      window.removeEventListener("message", receive)
      window.clearTimeout(timeout)
      window.opener = null
      window.name = `${windowPrefix}closed`
      resolve(result)
    }
    const receive = (event: MessageEvent) => {
      if (
        event.origin !== window.location.origin ||
        event.source !== opener ||
        event.data?.nonce !== nonce ||
        event.data.type !== "account-view-activate"
      )
        return
      try {
        const { selection, issued, returnPath, tenantID } = event.data
        if (
          !selection ||
          !["system", "platform", "tenant", "entity", "personal"].includes(
            selection.scope_type
          ) ||
          !["read_only", "read_write"].includes(selection.mode) ||
          issued?.mode !== selection.mode ||
          (selection.target_type === "platform" ||
          selection.target_type === "tenant"
            ? selection.principal_id !== undefined ||
              selection.scope_type !== selection.target_type
            : (selection.target_type !== undefined &&
                selection.target_type !== "account") ||
              typeof selection.principal_id !== "string") ||
          typeof selection.scope_id !== "string" ||
          typeof issued?.id !== "string" ||
          typeof issued?.token !== "string" ||
          typeof returnPath !== "string" ||
          typeof tenantID !== "string"
        )
          throw new Error("Invalid account view handoff")
        activateAccountView(selection, issued, returnPath, tenantID)
        window.history.replaceState(
          null,
          "",
          accountViewPath(selection, tenantID)
        )
        opener.postMessage(
          { type: "account-view-accepted", nonce },
          window.location.origin
        )
        finish("active")
      } catch {
        clearAccountView()
        finish("closed")
      }
    }
    const timeout = window.setTimeout(() => finish("closed"), 30_000)
    window.addEventListener("message", receive)
    opener.postMessage(
      { type: "account-view-ready", nonce },
      window.location.origin
    )
  })
}
