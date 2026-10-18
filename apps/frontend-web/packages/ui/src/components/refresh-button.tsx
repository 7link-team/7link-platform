import { useCallback, useEffect, useRef, useState } from "react"
import { ChevronDown, RefreshCw } from "lucide-react"
import { useTranslations } from "use-intl"

import { cn } from "@workspace/ui/lib/utils"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "./dropdown-menu"

const AUTO_REFRESH_SECONDS = [5, 10, 15, 30, 60] as const

export function RefreshButton({
  onRefresh,
  disabled = false,
  loading = false,
}: {
  onRefresh: () => void | Promise<void>
  disabled?: boolean
  loading?: boolean
}) {
  const t = useTranslations("table.filter")
  const [seconds, setSeconds] = useState(0)
  const [remaining, setRemaining] = useState(0)
  const [pending, setPending] = useState(false)
  const running = useRef(false)
  const deadline = useRef(0)
  const latest = useRef({ onRefresh, disabled, loading })
  useEffect(() => {
    latest.current = { onRefresh, disabled, loading }
  }, [onRefresh, disabled, loading])

  // Both manual and scheduled reads use the latest callback and share one lock.
  const refresh = useCallback(async () => {
    if (running.current || latest.current.disabled || latest.current.loading)
      return
    running.current = true
    setPending(true)
    try {
      await latest.current.onRefresh()
    } finally {
      running.current = false
      setPending(false)
    }
  }, [])

  useEffect(() => {
    if (!seconds) return
    const timer = window.setInterval(() => {
      const next = Math.max(
        0,
        Math.ceil((deadline.current - Date.now()) / 1000)
      )
      if (next === 0) {
        deadline.current = Date.now() + seconds * 1000
        setRemaining(seconds)
        // Keep errors observable to the caller's existing loading/error handler.
        // A rejected read must not create an unhandled timer rejection.
        void refresh().catch(() => {})
      } else {
        setRemaining(next)
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [seconds, refresh])

  const busy = loading || pending
  return (
    <div
      data-slot="refresh-button"
      className="inline-flex h-8 shrink-0 items-center overflow-hidden rounded-md border border-input bg-background text-sm shadow-sm"
    >
      <button
        type="button"
        aria-label={t("refresh")}
        title={t("refresh")}
        disabled={disabled || busy}
        onClick={() => {
          if (seconds) {
            deadline.current = Date.now() + seconds * 1000
            setRemaining(seconds)
          }
          void refresh().catch(() => {})
        }}
        className="inline-flex h-full items-center gap-1.5 px-3 tabular-nums transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50"
      >
        <RefreshCw className={cn("size-4", busy && "animate-spin")} />
        <span>{seconds ? t("secLeft", { s: remaining }) : t("refresh")}</span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={t("autoRefresh")}
          title={t("autoRefresh")}
          className="inline-flex h-full items-center border-l border-input px-1.5 transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-2 focus-visible:outline-ring data-[state=open]:bg-accent"
        >
          <ChevronDown className="size-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuLabel>{t("autoRefresh")}</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={String(seconds)}
            onValueChange={(value) => {
              const interval = Number(value)
              deadline.current = Date.now() + interval * 1000
              setSeconds(interval)
              setRemaining(interval)
            }}
          >
            <DropdownMenuRadioItem value="0" closeOnClick>
              {t("autoOff")}
            </DropdownMenuRadioItem>
            {AUTO_REFRESH_SECONDS.map((interval) => (
              <DropdownMenuRadioItem
                key={interval}
                value={String(interval)}
                closeOnClick
              >
                {t("autoEvery", { s: interval })}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
