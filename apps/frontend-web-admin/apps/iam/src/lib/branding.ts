/**
 * Display names shown in the console chrome.
 *
 * A build carries no brand of its own: the product and organization names are
 * deployment configuration. When nothing is configured the console falls back
 * to neutral wording rather than to whichever brand happened to be first.
 */

const readEnv = (value: string | undefined) => value?.trim() ?? ""

/** Console display name, e.g. "Acme IAM". Neutral when unconfigured. */
export const appName = readEnv(import.meta.env.VITE_APP_NAME) || "IAM"

/** Organization named in the footer. Empty means the footer shows only the year. */
export const organizationName = readEnv(import.meta.env.VITE_ORG_NAME)

/**
 * Footer line: "Acme · © 2026", or "© 2026" with no organization configured.
 *
 * The year is read at render time so the footer cannot go stale in a build that
 * outlives the calendar year it was produced in.
 */
export function copyrightLine(year = new Date().getFullYear()): string {
  return organizationName ? `${organizationName} · © ${year}` : `© ${year}`
}
