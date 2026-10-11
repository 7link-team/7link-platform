import { useSyncExternalStore } from "react"

import { iamApi, type DeploymentIdentity } from "./iam-api"

/**
 * Display names shown in the console chrome.
 *
 * A build carries no brand of its own: the product and organization names are
 * deployment configuration, read from the service so one build serves any
 * deployment. When nothing is configured the console falls back to neutral
 * wording rather than to whichever brand happened to be first.
 *
 * The last answer is remembered so a returning visitor paints the right name
 * immediately instead of watching it change once the request lands. The key
 * carries a scope from the first day, even though the scope is always
 * "default" today: should a deployment ever brand itself per tenant, the cache
 * can hold both without a migration.
 */
const cacheKey = "iam.deployment:default"

/** Shown when the deployment configures no name of its own. */
const neutral: DeploymentIdentity = { name: "IAM", organization: "" }

function read(): DeploymentIdentity {
  try {
    const raw = localStorage.getItem(cacheKey)
    if (!raw) return neutral
    const value: unknown = JSON.parse(raw)
    if (typeof value !== "object" || value === null) return neutral
    const { name, organization } = value as Partial<DeploymentIdentity>
    if (typeof name !== "string" || typeof organization !== "string")
      return neutral
    return { name: name.trim() || neutral.name, organization }
  } catch {
    // Private windows, cleared site data and blocked storage all land here.
    // The name is a display string; losing the cache costs one repaint.
    return neutral
  }
}

let current = read()
const listeners = new Set<() => void>()

function publish(next: DeploymentIdentity) {
  if (next.name === current.name && next.organization === current.organization)
    return
  current = next
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/**
 * Read the deployment's identity once, and remember it.
 *
 * Called at start-up, before a session exists, because the sign-in page needs
 * it too. A failure leaves the last known answer in place -- the console must
 * still render when the service cannot be reached, and a missing display name
 * is not a reason to show nobody the login form.
 */
export async function loadBranding(): Promise<void> {
  try {
    const identity = await iamApi.deployment()
    const next: DeploymentIdentity = {
      name: identity.name.trim() || neutral.name,
      organization: identity.organization.trim(),
    }
    publish(next)
    try {
      localStorage.setItem(cacheKey, JSON.stringify(next))
    } catch {
      // Storage is a convenience here; the value is already published.
    }
  } catch {
    // Keep whatever is displayed. The next load asks again.
  }
}

/** The deployment's identity as last known. Safe before the first answer. */
export const branding = () => current

/** Re-renders the caller when the deployment's identity arrives or changes. */
export function useBranding(): DeploymentIdentity {
  return useSyncExternalStore(subscribe, branding, branding)
}

/**
 * Footer line: "Acme · © 2026", or "© 2026" with no organization configured.
 *
 * The year is read at render time so the footer cannot go stale in a build that
 * outlives the calendar year it was produced in.
 */
export function copyrightLine(
  organization = current.organization,
  year = new Date().getFullYear()
): string {
  return organization ? `${organization} · © ${year}` : `© ${year}`
}
