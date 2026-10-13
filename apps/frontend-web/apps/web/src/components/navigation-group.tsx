import { useState, type ReactNode } from "react"
import { ChevronDown, Folder } from "lucide-react"

export function NavigationGroup({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  const [expanded, setExpanded] = useState(true)
  return (
    <div data-navigation-group>
      <button
        type="button"
        className="flex min-h-11 w-full min-w-0 items-center gap-2 rounded-md px-3 py-2 text-start text-sm font-medium hover:bg-sidebar-accent"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
      >
        <Folder className="size-4 shrink-0" />
        <span className="min-w-0 flex-1 break-words">{label}</span>
        <ChevronDown
          className={`size-4 shrink-0 transition-transform ${expanded ? "" : "-rotate-90"}`}
        />
      </button>
      {expanded && (
        <div
          className="ms-4 space-y-1 border-s border-sidebar-border ps-2"
          data-navigation-children
        >
          {children}
        </div>
      )}
    </div>
  )
}
