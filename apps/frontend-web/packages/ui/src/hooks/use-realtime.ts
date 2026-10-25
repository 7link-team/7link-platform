import { createContext, createElement, useContext, useEffect, useEffectEvent, useMemo, useSyncExternalStore, type ReactNode } from "react"
import type { RealtimeClient, RealtimeEvent, RealtimeSnapshot } from "../lib/realtime"

const RealtimeContext = createContext<{ client: RealtimeClient; tenantID: string } | null>(null)
const unavailable: RealtimeSnapshot = { status: "offline" }
const noSubscription = () => () => {}
const unavailableSnapshot = () => unavailable

export function RealtimeProvider({ client, tenantID, children }: { client: RealtimeClient; tenantID: string; children: ReactNode }) {
  const value = useMemo(() => ({ client, tenantID }), [client, tenantID])
  return createElement(RealtimeContext.Provider, { value }, children)
}

export function useRealtime(
  topic: string | string[] | null,
  options: { onEvent?: (event: RealtimeEvent) => void; eventTypes?: string[]; onResync?: () => void; poll?: () => unknown; pollIntervalMs?: number } = {},
): RealtimeSnapshot {
  const context = useContext(RealtimeContext)
  const key = JSON.stringify(topic === null ? [] : typeof topic === "string" ? [topic] : topic)
  const event = useEffectEvent((value: RealtimeEvent) => {
    if (!options.eventTypes || options.eventTypes.includes(value.type)) options.onEvent?.(value)
  })
  const resync = useEffectEvent(() => options.onResync?.())
  const poll = useEffectEvent(() => {
    try { void Promise.resolve(options.poll?.()).catch(() => {}) } catch { /* The page owns its load-error UI. */ }
  })
  const snapshot = useSyncExternalStore(context?.client.subscribeStatus ?? noSubscription, context?.client.getSnapshot ?? unavailableSnapshot, unavailableSnapshot)
  useEffect(() => {
    if (!context?.tenantID) return
    const topics = JSON.parse(key) as string[]
    if (!topics.length) return
    return context.client.subscribe(context.tenantID, topics, { onEvent: event, onResync: resync })
  }, [context, key])
  const interval = options.pollIntervalMs ?? 10_000
  const canPoll = Boolean(options.poll)
  useEffect(() => {
    if (!canPoll || snapshot.status !== "polling" || !context?.tenantID) return
    poll()
    const timer = setInterval(poll, interval)
    return () => clearInterval(timer)
  }, [canPoll, context?.tenantID, interval, snapshot.status])
  return snapshot
}
