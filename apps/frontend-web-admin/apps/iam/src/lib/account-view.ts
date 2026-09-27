export type AccountViewScope =
  "system" | "platform" | "tenant" | "entity" | "personal"
export type AccountViewMode = "read_only" | "read_write"

export interface AccountViewSelection {
  principal_id: string
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
const listeners = new Set<() => void>()

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
  if (!current) return { credentials: "include" }
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
  const response = await fetch(
    `${(import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")}/account-views/${encodeURIComponent(view.id)}`,
    {
      method: "DELETE",
      credentials: "include",
      headers: { Origin: window.location.origin },
    }
  )
  if (!response.ok && response.status !== 401 && response.status !== 404)
    throw new Error("Unable to close account view")
  window.location.replace(view.returnPath)
}

export async function issueAccountView(selection: AccountViewSelection) {
  if (current) throw new Error("An account view is already open")
  const response = await fetch(
    `${(import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")}/account-views`,
    {
      method: "POST",
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
