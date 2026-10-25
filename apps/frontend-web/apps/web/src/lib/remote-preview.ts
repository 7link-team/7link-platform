// Preview application bytes are served only by the independently configured
// gateway. The console receives a short-lived access URL from its own API.
export function previewAccessURL(value: string, consoleOrigin: string): string {
  const url = new URL(value)
  if (url.protocol !== "https:" || url.origin === new URL(consoleOrigin).origin || url.username || url.password) {
    throw new Error("invalid_response")
  }
  return url.href
}

export function previewLabel(search: string): string | null {
  const labels = new URLSearchParams(search).getAll("label")
  return labels.length === 1 && /^[a-z2-7]{26}$/.test(labels[0] ?? "") ? labels[0]! : null
}

export function previewPort(value: string): number | null {
  if (!/^\d{1,5}$/.test(value)) return null
  const port = Number(value)
  return port >= 1 && port <= 65535 ? port : null
}
