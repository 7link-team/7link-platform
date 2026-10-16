/** Transport-neutral, client-safe validation details from the error envelope. */
export interface ValidationIssue {
  in: "body" | "query" | "path" | "header" | "cookie"
  path?: string
  location?: string
  code: string
  message_key?: string
  params?: Record<string, string | number | boolean | null>
  message: string
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)

/** Copy only the public issue contract; never retain rejected values or arbitrary payloads. */
export function normalizeValidationIssues(data: unknown): ValidationIssue[] {
  if (!object(data) || !Array.isArray(data.validation_errors)) return []
  return data.validation_errors.flatMap((raw): ValidationIssue[] => {
    if (
      !object(raw) ||
      typeof raw.in !== "string" ||
      !["body", "query", "path", "header", "cookie"].includes(raw.in) ||
      typeof raw.code !== "string" ||
      !raw.code ||
      typeof raw.message !== "string" ||
      !raw.message
    )
      return []
    const issue: ValidationIssue = {
      in: raw.in as ValidationIssue["in"],
      code: raw.code,
      message: raw.message,
    }
    if (typeof raw.path === "string") issue.path = raw.path
    if (typeof raw.location === "string") issue.location = raw.location
    if (typeof raw.message_key === "string") issue.message_key = raw.message_key
    if (object(raw.params)) {
      issue.params = Object.fromEntries(
        Object.entries(raw.params).filter(
          ([, v]) =>
            v === null ||
            typeof v === "string" ||
            typeof v === "boolean" ||
            (typeof v === "number" && Number.isFinite(v))
        )
      ) as ValidationIssue["params"]
    }
    return [issue]
  })
}

/** AutoForm currently has flat field keys. Bind only an exact top-level body pointer.
 * Nested, non-body, hidden and unknown fields remain visible at form level.
 * Huma's dotted location is intentionally never interpreted as a JSON pointer.
 */
export function validationFormErrors(
  issues: readonly ValidationIssue[],
  fields: ReadonlySet<string>,
  catalog?: Readonly<Record<string, ValidationMessage>>
): Record<string, string> {
  const messages = new Map<string, string[]>()
  for (const issue of issues) {
    let field = "_form"
    if (issue.in === "body" && issue.path?.startsWith("/")) {
      const token = issue.path.slice(1)
      if (!token.includes("/") && !/~(?![01])/u.test(token)) {
        const decoded = token.replaceAll("~1", "/").replaceAll("~0", "~")
        if (fields.has(decoded) && decoded !== "_form") field = decoded
      }
    }
    const template =
      issue.message_key && catalog && Object.hasOwn(catalog, issue.message_key)
        ? catalog[issue.message_key]
        : undefined
    const message =
      template?.code === issue.code &&
      template.message_key === issue.message_key
        ? (renderValidationMessage(template, issue.params) ?? issue.message)
        : issue.message
    const current = messages.get(field) ?? []
    if (!current.includes(message)) current.push(message)
    messages.set(field, current)
  }
  return Object.fromEntries(
    [...messages].map(([key, values]) => [key, values.join("\n")])
  )
}

export interface ValidationMessage {
  code: string
  message_key: string
  template: string
}

/** Literal substitution only. Missing/invalid parameters never leak a template. */
export function renderValidationMessage(
  raw: unknown,
  params: Record<string, unknown> = {}
): string | undefined {
  if (
    !object(raw) ||
    typeof raw.code !== "string" ||
    !raw.code ||
    typeof raw.message_key !== "string" ||
    !raw.message_key ||
    typeof raw.template !== "string" ||
    !raw.template
  )
    return undefined
  let valid = true
  const rendered = raw.template.replace(
    /\{([A-Za-z][A-Za-z0-9_]*)\}/g,
    (_match, name: string) => {
      if (!Object.hasOwn(params, name)) {
        valid = false
        return ""
      }
      const value = params[name]
      if (
        typeof value === "string" ||
        typeof value === "boolean" ||
        (typeof value === "number" && Number.isFinite(value))
      )
        return String(value)
      if (value === null) return "null"
      if (
        Array.isArray(value) &&
        value.every(
          (item) =>
            item === null ||
            typeof item === "string" ||
            typeof item === "boolean" ||
            (typeof item === "number" && Number.isFinite(item))
        )
      )
        return JSON.stringify(value)
      valid = false
      return ""
    }
  )
  // Inspect the template, not substituted text: values may legitimately contain braces.
  if (/[{}]/.test(raw.template.replace(/\{[A-Za-z][A-Za-z0-9_]*\}/g, "")))
    valid = false
  return valid ? rendered : undefined
}
