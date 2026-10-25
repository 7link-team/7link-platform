import type { RealtimeStatus as Status } from "../lib/realtime"
import { cn } from "../lib/utils"

export function RealtimeStatus({ status, labels }: { status: Status; labels: Record<Exclude<Status, "live">, string> }) {
  if (status === "live") return null
  const label = labels[status]
  return <span role="status" aria-atomic="true" aria-label={label} title={label} className="inline-flex min-h-11 min-w-11 items-center justify-center gap-2 px-2 text-xs text-muted-foreground">
    <span aria-hidden="true" className={cn("size-2 shrink-0 rounded-full", status === "connecting" && "bg-primary motion-safe:animate-pulse", status === "polling" && "bg-amber-500", status === "offline" && "border border-dashed border-muted-foreground")} />
    <span className="hidden xl:inline">{label}</span>
  </span>
}
