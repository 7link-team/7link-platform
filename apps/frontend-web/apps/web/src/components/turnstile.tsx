import { useEffect, useRef } from "react"
import { useLocale } from "@workspace/ui/i18n/intl-provider"

/**
 * The Cloudflare Turnstile widget.
 *
 * Only rendered when the service says the deployment delegates its challenge,
 * so a deployment that draws its own never loads anything from Cloudflare --
 * which is the point of asking the service rather than deciding in the client.
 *
 * The script is fetched once per document and shared: two forms on one page,
 * or a remount, must not pull it again. A failure to load is reported like a
 * failure to obtain a challenge, because to the person in front of the form it
 * is the same thing.
 */
const scriptURL =
  "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit"

interface TurnstileAPI {
  render: (
    element: HTMLElement,
    options: {
      sitekey: string
      language?: string
      callback: (token: string) => void
      "expired-callback": () => void
      "error-callback": () => void
    }
  ) => string
  remove: (id: string) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileAPI
  }
}

let pending: Promise<TurnstileAPI> | undefined

function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile)
  pending ??= new Promise<TurnstileAPI>((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(
      `script[src="${scriptURL}"]`
    )
    const script = existing ?? document.createElement("script")
    const settle = () =>
      window.turnstile
        ? resolve(window.turnstile)
        : reject(new Error("turnstile unavailable"))
    script.addEventListener("load", settle)
    script.addEventListener("error", () =>
      reject(new Error("turnstile unavailable"))
    )
    if (!existing) {
      script.src = scriptURL
      script.async = true
      document.head.append(script)
    }
  }).catch((cause: unknown) => {
    // A failed load must not poison every later attempt: refreshing the
    // challenge is the one action left to somebody stuck here.
    pending = undefined
    throw cause
  })
  return pending
}

export function Turnstile({
  siteKey,
  onToken,
  onUnavailable,
}: {
  siteKey: string
  /** Called with a solved token, and with "" when one expires or errors. */
  onToken: (token: string) => void
  onUnavailable: () => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const locale = useLocale()
  // The callbacks are read through a ref so a re-render with a new closure
  // does not tear the widget down and ask Cloudflare for another challenge.
  const handlers = useRef({ onToken, onUnavailable })
  useEffect(() => {
    handlers.current = { onToken, onUnavailable }
  }, [onToken, onUnavailable])

  useEffect(() => {
    let widget: string | undefined
    let active = true
    void loadTurnstile().then(
      (turnstile) => {
        if (!active || !container.current) return
        widget = turnstile.render(container.current, {
          sitekey: siteKey,
          language: locale,
          callback: (token) => handlers.current.onToken(token),
          "expired-callback": () => handlers.current.onToken(""),
          "error-callback": () => handlers.current.onUnavailable(),
        })
      },
      () => {
        if (active) handlers.current.onUnavailable()
      }
    )
    return () => {
      active = false
      if (widget && window.turnstile) window.turnstile.remove(widget)
    }
  }, [siteKey, locale])

  return <div ref={container} data-testid="turnstile" />
}
