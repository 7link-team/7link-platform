import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { RealtimeStatus } from "./realtime-status"

it("announces contextual connection state and respects reduced motion", () => {
  const labels = { connecting: "Connecting", polling: "Refreshing every 10 seconds", offline: "Offline" }
  expect(renderToStaticMarkup(<RealtimeStatus status="live" labels={labels} />)).toBe("")
  const connecting = renderToStaticMarkup(<RealtimeStatus status="connecting" labels={labels} />)
  expect(connecting).toContain('role="status"')
  expect(connecting).toContain('aria-atomic="true"')
  expect(connecting).toContain("motion-safe:animate-pulse")
  expect(renderToStaticMarkup(<RealtimeStatus status="polling" labels={labels} />)).toContain('aria-label="Refreshing every 10 seconds"')
  expect(renderToStaticMarkup(<RealtimeStatus status="offline" labels={labels} />)).toContain("border-dashed")
})
