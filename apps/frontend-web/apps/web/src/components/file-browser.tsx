import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import {
  useRef,
  useState,
  useSyncExternalStore,
  type DragEvent,
  type Dispatch,
  type KeyboardEvent,
  type ReactNode,
  type SetStateAction,
} from "react"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ChevronRight,
  ClipboardPaste,
  Copy,
  Download,
  Eye,
  ExternalLink,
  FileCode2,
  FilePlus2,
  FileText,
  Folder,
  FolderPlus,
  FolderOpen,
  Grid2X2,
  Info,
  List,
  LoaderCircle,
  MoreVertical,
  PanelLeft,
  Pencil,
  RefreshCw,
  RotateCcw,
  Scissors,
  Search,
  SquareCheckBig,
  Trash2,
  Upload,
  X,
} from "lucide-react"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@workspace/ui/components/context-menu"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetTrigger,
} from "@workspace/ui/components/sheet"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@workspace/ui/components/tooltip"
import {
  fileSizeLabel,
  parseDroppedEntries,
  selectFileRange,
  visibleFileNodes,
  type DroppedDataTransferItem,
  type DroppedEntry,
  type FileBrowserLocation,
  type FileBrowserNode,
  type FileBrowserRoot,
  type FileBrowserRootScope,
  type FileBrowserSort,
} from "../lib/file-browser"
import { FilePreviewDialog } from "./file-preview"
import { FileTypeIcon, FileVisual } from "./file-type-icon"

export type FileBrowserProps = {
  roots: readonly FileBrowserRoot[]
  path: readonly FileBrowserLocation[]
  nodes: readonly FileBrowserNode[]
  status:
    "ready" | "loading" | "selectEntity" | "unavailable" | "forbidden" | "error"
  onNavigate?: (id: string) => void
  onBack?: () => void
  onForward?: () => void
  onRefresh?: () => void
  onNextPage?: () => void
  onPreviousPage?: () => void
  busy?: boolean
  actionError?: string
  clipboard?: { action: "copy" | "move"; count: number }
  recycleBin?: boolean
  onOpenRecycleBin?: () => void
  personal?: boolean
  onEnsureSystemRoot?: () => Promise<boolean>
  onCreateDirectory?: (name: string) => Promise<boolean>
  onCreateFile?: (name: string, mimeType: string) => Promise<boolean>
  onUploadEntries?: (entries: readonly DroppedEntry[]) => Promise<boolean>
  onPreview?: (
    node: FileBrowserNode,
    maxBytes: number,
    signal: AbortSignal
  ) => Promise<Blob>
  onDownload?: (node: FileBrowserNode) => Promise<boolean>
  onDownloadSelected?: (nodes: readonly FileBrowserNode[]) => Promise<boolean>
  onRename?: (node: FileBrowserNode, name: string) => Promise<boolean>
  onTrash?: (node: FileBrowserNode) => Promise<boolean>
  onTrashSelected?: (nodes: readonly FileBrowserNode[]) => Promise<boolean>
  onCopy?: (nodes: readonly FileBrowserNode[]) => void
  onCut?: (nodes: readonly FileBrowserNode[]) => void
  onPaste?: () => Promise<boolean>
  initialPreviewID?: string
  onClosePreview?: () => void
  onOpenNewTab?: (node: FileBrowserNode) => void
  onMoveTo?: (
    nodes: readonly FileBrowserNode[],
    destination: FileBrowserNode
  ) => Promise<boolean>
  onRestore?: (
    node: FileBrowserNode,
    name?: string
  ) => Promise<"restored" | "name-conflict" | "failed">
}

const rootScopeOrder: readonly FileBrowserRootScope[] = [
  "system",
  "personal",
  "account",
  "tenant",
  "entity",
  "department",
]

function rootScopeMessage(scope: FileBrowserRootScope) {
  switch (scope) {
    case "system":
      return "rootScopeSystem"
    case "personal":
      return "rootScopePersonal"
    case "account":
      return "rootScopeAccount"
    case "tenant":
      return "rootScopeTenant"
    case "entity":
      return "rootScopeEntity"
    case "department":
      return "rootScopeDepartment"
  }
}

function rootNameMessage(scope: FileBrowserRootScope) {
  switch (scope) {
    case "system":
      return "rootNameSystem"
    case "personal":
      return "rootNamePersonal"
    case "account":
      return "rootNameAccount"
    case "tenant":
      return "rootNameTenant"
    case "entity":
      return "rootNameEntity"
    case "department":
      return "rootNameDepartment"
  }
}

function rootLabel(
  root: FileBrowserRoot,
  translate: (key: string) => string
): string {
  return root.ownerName?.trim() || translate(rootNameMessage(root.scope))
}

function subscribeWide(onChange: () => void) {
  const media = window.matchMedia("(min-width: 1280px)")
  media.addEventListener("change", onChange)
  return () => media.removeEventListener("change", onChange)
}

function Tool({
  label,
  children,
  active,
  disabled,
  onClick,
}: {
  label: string
  children: ReactNode
  active?: boolean
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant={active ? "secondary" : "ghost"}
            size="icon"
            className="size-11 shrink-0"
            aria-label={label}
            aria-pressed={active}
            disabled={disabled}
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

// A location/page change remounts the body so item selections and metadata do
// not cross authorization boundaries. The explicit selection mode stays active.
export function FileBrowser(props: FileBrowserProps) {
  const [selectionMode, setSelectionMode] = useState(false)
  return (
    <FileBrowserBody
      key={JSON.stringify([
        props.path.map((item) => item.id),
        props.nodes.map((item) => item.id),
        props.status,
        props.recycleBin,
        props.initialPreviewID,
      ])}
      {...props}
      selectionMode={selectionMode}
      setSelectionMode={setSelectionMode}
    />
  )
}

function FileBrowserBody({
  roots,
  path,
  nodes,
  status,
  onNavigate,
  onBack,
  onForward,
  onRefresh,
  onNextPage,
  onPreviousPage,
  busy = false,
  actionError = "",
  clipboard,
  recycleBin = false,
  onOpenRecycleBin,
  personal = false,
  onEnsureSystemRoot,
  onCreateDirectory,
  onCreateFile,
  onUploadEntries,
  onPreview,
  onDownload,
  onDownloadSelected,
  onRename,
  onTrash,
  onTrashSelected,
  onCopy,
  onCut,
  onPaste,
  initialPreviewID,
  onClosePreview,
  onOpenNewTab,
  onMoveTo,
  onRestore,
  selectionMode,
  setSelectionMode,
}: FileBrowserProps & {
  selectionMode: boolean
  setSelectionMode: Dispatch<SetStateAction<boolean>>
}) {
  const tf = useTranslations("files")
  const locale = useLocale()
  const wide = useSyncExternalStore(
    subscribeWide,
    () => window.matchMedia("(min-width: 1280px)").matches,
    () => false
  )
  const [view, setView] = useState<"details" | "small" | "large">("details")
  const [query, setQuery] = useState("")
  const [kind, setKind] = useState<"all" | FileBrowserNode["kind"]>("all")
  const [sort, setSort] = useState<FileBrowserSort>("name")
  const [descending, setDescending] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [anchor, setAnchor] = useState<string | null>(null)
  const [detailsID, setDetailsID] = useState<string | null>(null)
  const [previewNode, setPreviewNode] = useState<FileBrowserNode | null>(
    () =>
      nodes.find(
        (node) => node.id === initialPreviewID && node.kind === "file"
      ) ?? null
  )
  const propertyTitle = useRef<HTMLHeadingElement>(null)
  const details = nodes.find((node) => node.id === detailsID) ?? null
  const [treeOpen, setTreeOpen] = useState(false)
  const [editor, setEditor] = useState<
    | { kind: "directory"; name: string }
    | {
        kind: "file"
        name: string
        extension: ".txt" | ".md"
        mimeType: "text/plain" | "text/markdown"
        title: "createTextFile" | "createMarkdownFile"
      }
    | { kind: "rename"; node: FileBrowserNode; name: string }
    | {
        kind: "restoreRename"
        node: FileBrowserNode
        name: string
        pending: FileBrowserNode[]
      }
    | { kind: "trash"; node: FileBrowserNode }
    | { kind: "trashSelection"; nodes: FileBrowserNode[] }
    | null
  >(null)
  const uploadInput = useRef<HTMLInputElement>(null)
  const dragDepth = useRef(0)
  const movingNodes = useRef<FileBrowserNode[]>([])
  const [dragActive, setDragActive] = useState(false)
  const [moveTargetID, setMoveTargetID] = useState<string | null>(null)
  const [dropError, setDropError] = useState("")
  // This presentation stage uses bounded pages, not a whole-space in-memory tree.
  const validPage =
    nodes.length <= 100 &&
    new Set(nodes.map((node) => node.id)).size === nodes.length
  const ready = status === "ready" && validPage
  const visiblePath = ready ? path : []
  const rows = ready
    ? visibleFileNodes(nodes, query, kind, sort, descending, locale)
    : []
  const visibleSelected = rows.filter((node) => selected.has(node.id))
  const selectedNodes = ready
    ? nodes.filter((node) => selected.has(node.id))
    : []
  const selectedFiles = selectedNodes.filter((node) => node.kind === "file")
  const contextNodes = (node: FileBrowserNode) =>
    selected.has(node.id) && selectedNodes.length > 1 ? selectedNodes : [node]
  const restorePathLabel = (node: FileBrowserNode) =>
    node.restorePath
      ?.map((segment) => {
        const root = roots.find((candidate) => candidate.id === segment.id)
        return root ? rootLabel(root, tf) : segment.name
      })
      .join(" / ")
  const directories = ready
    ? nodes.filter((node) => node.kind === "directory")
    : []
  const rootGroups = rootScopeOrder
    .map((scope) => ({
      scope,
      roots: (ready ? roots : []).filter((root) => root.scope === scope),
    }))
    .filter((group) => group.roots.length > 0)
  const navigate = (id: string) => {
    setTreeOpen(false)
    setSelected(new Set())
    setAnchor(null)
    onNavigate?.(id)
  }
  const open = (node: FileBrowserNode) => {
    if (node.kind === "directory" && !recycleBin) {
      navigate(node.id)
      return
    }
    if (node.kind === "file" && !recycleBin && onPreview) {
      setPreviewNode(node)
      return
    }
    setDetailsID(node.id)
  }
  const activate = (node: FileBrowserNode) => {
    if (node.kind === "directory" && !recycleBin) {
      if (selectionMode) select(node.id)
      else {
        setSelected(new Set([node.id]))
        setAnchor(node.id)
      }
      return
    }
    open(node)
  }
  const openDirectoryWithKeyboard = (
    event: KeyboardEvent<HTMLButtonElement>,
    node: FileBrowserNode
  ) => {
    if (event.key !== "Enter" || node.kind !== "directory" || recycleBin) return
    event.preventDefault()
    open(node)
  }
  const startMove = (event: DragEvent, node: FileBrowserNode) => {
    if (busy || recycleBin || !onMoveTo) return
    const sources = contextNodes(node)
    movingNodes.current = sources
    event.dataTransfer.effectAllowed = "move"
    event.dataTransfer.setData(
      "application/x-file-browser-items",
      sources.map((source) => source.id).join(",")
    )
  }
  const moveOver = (event: DragEvent, destination: FileBrowserNode) => {
    if (
      destination.kind !== "directory" ||
      !movingNodes.current.length ||
      movingNodes.current.some((source) => source.id === destination.id)
    )
      return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = "move"
    setMoveTargetID(destination.id)
  }
  const moveInto = async (event: DragEvent, destination: FileBrowserNode) => {
    if (moveTargetID !== destination.id || !movingNodes.current.length) return
    event.preventDefault()
    event.stopPropagation()
    const sources = movingNodes.current
    movingNodes.current = []
    setMoveTargetID(null)
    if (await onMoveTo?.(sources, destination)) {
      setSelected(new Set())
      setAnchor(null)
    }
  }
  const finishMove = () => {
    movingNodes.current = []
    setMoveTargetID(null)
  }
  const select = (id: string, range = false) => {
    setSelected((previous) =>
      selectFileRange(previous, rows, id, anchor, range)
    )
    if (!range || !anchor) setAnchor(id)
  }
  const submitEditor = async (values: Record<string, unknown>) => {
    if (!editor || busy) return
    if (editor.kind === "directory") {
      if (await onCreateDirectory?.(String(values.name ?? "").trim()))
        setEditor(null)
      return
    }
    if (editor.kind === "file") {
      const trimmed = String(values.name ?? "").trim()
      const name = trimmed.toLowerCase().endsWith(editor.extension)
        ? trimmed
        : `${trimmed}${editor.extension}`
      if (await onCreateFile?.(name, editor.mimeType)) setEditor(null)
      return
    }
    if (editor.kind === "rename") {
      if (await onRename?.(editor.node, String(values.name ?? "").trim()))
        setEditor(null)
      return
    }
    if (editor.kind === "restoreRename") {
      const result = await onRestore?.(
        editor.node,
        String(values.name ?? "").trim()
      )
      if (result === "restored") {
        const pending = editor.pending
        setSelected((previous) => {
          const next = new Set(previous)
          next.delete(editor.node.id)
          return next
        })
        setEditor(null)
        if (pending.length) await restoreNodes(pending)
      }
    }
  }
  const restoreNodes = async (items: readonly FileBrowserNode[]) => {
    if (!onRestore || busy) return
    for (let index = 0; index < items.length; index += 1) {
      const node = items[index]!
      const result = await onRestore(node)
      if (result === "name-conflict") {
        setEditor({
          kind: "restoreRename",
          node,
          name: node.name,
          pending: items.slice(index + 1),
        })
        return
      }
      if (result === "failed") return
      setSelected((previous) => {
        const next = new Set(previous)
        next.delete(node.id)
        return next
      })
    }
  }
  const confirmTrash = async () => {
    if (busy) return
    if (editor?.kind === "trash") {
      if (await onTrash?.(editor.node)) setEditor(null)
      return
    }
    if (editor?.kind === "trashSelection") {
      if (await onTrashSelected?.(editor.nodes)) {
        setSelected(new Set())
        setEditor(null)
      }
    }
  }
  const writeLocation = visiblePath.length > 0 || (personal && !recycleBin)
  const canDrop = ready && writeLocation && !busy && Boolean(onUploadEntries)
  const hasDraggedFiles = (event: DragEvent) =>
    Array.from(event.dataTransfer.types).includes("Files")
  const startDrag = (event: DragEvent) => {
    if (!canDrop || !hasDraggedFiles(event)) return
    event.preventDefault()
    dragDepth.current += 1
    setDragActive(true)
  }
  const continueDrag = (event: DragEvent) => {
    if (!canDrop || !hasDraggedFiles(event)) return
    event.preventDefault()
    event.dataTransfer.dropEffect = "copy"
  }
  const leaveDrag = (event: DragEvent) => {
    if (!dragActive) return
    event.preventDefault()
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (!dragDepth.current) setDragActive(false)
  }
  const drop = async (event: DragEvent) => {
    if (!canDrop || !hasDraggedFiles(event)) return
    event.preventDefault()
    dragDepth.current = 0
    setDragActive(false)
    setDropError("")
    const items = Array.from(
      event.dataTransfer.items
    ) as DroppedDataTransferItem[]
    const files = Array.from(event.dataTransfer.files)
    try {
      const entries = await parseDroppedEntries(items, files)
      if (entries.length) await onUploadEntries?.(entries)
    } catch (cause) {
      const code = cause instanceof Error ? cause.message : "drop_read_failed"
      setDropError(
        tf(
          code === "drop_too_deep" ||
            code === "drop_too_many_entries" ||
            code === "drop_read_failed"
            ? code
            : "drop_read_failed"
        )
      )
    }
  }
  const date = (value?: string) =>
    value && Number.isFinite(Date.parse(value))
      ? new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(
          new Date(value)
        )
      : "\u2014"
  const navigation = (
    <nav
      aria-label={tf("roots")}
      className="flex h-full min-h-0 flex-col gap-4 p-3"
    >
      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto">
        <h2 className="text-xs font-semibold text-muted-foreground">
          {tf("roots")}
        </h2>
        {rootGroups.map((group) => (
          <div key={group.scope} className="space-y-1">
            <h3 className="px-2 text-xs font-medium text-muted-foreground">
              {tf(rootScopeMessage(group.scope))}
            </h3>
            {group.roots.map((root) => (
              <Button
                key={root.id}
                data-root-id={root.id}
                variant={path[0]?.id === root.id ? "secondary" : "ghost"}
                className="min-h-11 w-full justify-start"
                disabled={!onNavigate}
                onClick={() => navigate(root.id)}
              >
                <FolderOpen className="shrink-0" />
                <span className="min-w-0 truncate text-start">
                  {rootLabel(root, tf)}
                </span>
              </Button>
            ))}
          </div>
        ))}
        {ready && !recycleBin && !roots.length && (
          <p className="text-sm text-muted-foreground">{tf("noRoots")}</p>
        )}
        {!recycleBin && directories.length > 0 && (
          <>
            <h2 className="text-xs font-semibold text-muted-foreground">
              {tf("folders")}
            </h2>
            {directories.map((node) => (
              <Button
                key={node.id}
                variant="ghost"
                className="min-h-11 w-full justify-start"
                disabled={!onNavigate}
                onClick={() => navigate(node.id)}
              >
                <Folder className="shrink-0 text-amber-600" />
                <span className="truncate">{node.name}</span>
              </Button>
            ))}
          </>
        )}
      </div>
      {ready && (
        <div className="shrink-0 border-t pt-3">
          <Button
            variant={recycleBin ? "secondary" : "ghost"}
            className="min-h-11 w-full justify-start"
            disabled={!onOpenRecycleBin}
            onClick={() => onOpenRecycleBin?.()}
          >
            <Trash2 className="shrink-0" />
            <span className="truncate">{tf("recycleBin")}</span>
          </Button>
        </div>
      )}
    </nav>
  )
  const properties = details && (
    <div className="space-y-5 p-4">
      <div className="flex items-start justify-between gap-2">
        <h2
          ref={propertyTitle}
          tabIndex={-1}
          className="min-w-0 text-base font-semibold break-all"
        >
          {details.name}
        </h2>
        <Tool label={tf("closeProperties")} onClick={() => setDetailsID(null)}>
          <X />
        </Tool>
      </div>
      <div className="flex h-24 items-center justify-center bg-muted/30">
        <FileTypeIcon node={details} className="size-12" showBadge />
      </div>
      <dl className="space-y-4 text-sm">
        {[
          [tf("type"), tf(details.kind)],
          [
            tf("size"),
            details.kind === "file"
              ? fileSizeLabel(details.size, locale)
              : "\u2014",
          ],
          [tf("updated"), date(details.updatedAt)],
          ...(details.detectedType
            ? [[tf("detectedType"), details.detectedType]]
            : []),
          ...(details.extension ? [[tf("extension"), details.extension]] : []),
          ...(details.version ? [[tf("version"), details.version]] : []),
          ...(recycleBin
            ? [
                [
                  tf("restoreLocation"),
                  restorePathLabel(details) ?? tf("restoreLocationUnavailable"),
                ],
              ]
            : []),
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="mt-1 break-all">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
  return (
    <TooltipProvider>
      <section aria-label={tf("title")} className="h-full min-h-0 min-w-0">
        {(actionError || dropError) && (
          <div
            role="alert"
            className="border-b border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive"
          >
            {actionError || dropError}
          </div>
        )}
        <div
          className={`relative flex h-full min-h-0 min-w-0 border-y ${dragActive ? "outline-2 -outline-offset-2 outline-primary" : ""}`}
          onDragEnter={startDrag}
          onDragOver={continueDrag}
          onDragLeave={leaveDrag}
          onDrop={(event) => void drop(event)}
        >
          {dragActive && (
            <div className="pointer-events-none absolute inset-3 z-40 grid place-items-center border-2 border-dashed border-primary bg-background/90 text-sm font-medium text-primary">
              {tf("dropToUpload")}
            </div>
          )}
          <aside className="hidden h-full min-h-0 w-48 shrink-0 overflow-hidden border-e lg:block">
            {navigation}
          </aside>
          <div className="flex min-w-0 flex-1 flex-col">
            <div className="flex min-h-14 min-w-0 flex-wrap items-center gap-1 border-b px-2">
              <Sheet open={treeOpen} onOpenChange={setTreeOpen}>
                <SheetTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-11 lg:hidden"
                      aria-label={tf("openFolders")}
                      disabled={busy || !ready}
                    />
                  }
                >
                  <PanelLeft />
                </SheetTrigger>
                <SheetContent side="left" className="w-72 overflow-y-auto p-0">
                  <SheetTitle className="px-3 pt-5">{tf("roots")}</SheetTitle>
                  {navigation}
                </SheetContent>
              </Sheet>
              <div className="flex shrink-0 items-center">
                <Tool
                  label={tf("back")}
                  disabled={!onBack || busy}
                  onClick={() => onBack?.()}
                >
                  <ArrowLeft />
                </Tool>
                <Tool
                  label={tf("forward")}
                  disabled={!onForward || busy}
                  onClick={() => onForward?.()}
                >
                  <ArrowRight />
                </Tool>
              </div>
              <nav
                aria-label={tf("path")}
                className="flex min-w-40 flex-1 flex-wrap items-center gap-1 py-2"
              >
                {recycleBin ? (
                  <span className="px-2 text-sm font-medium">
                    {tf("recycleBin")}
                  </span>
                ) : visiblePath.length ? (
                  visiblePath.map((item, index) => (
                    <div
                      key={item.id}
                      className="flex max-w-full min-w-0 items-center"
                    >
                      <Button
                        variant="ghost"
                        className="min-h-11 max-w-full min-w-0"
                        aria-current={
                          index === visiblePath.length - 1 ? "page" : undefined
                        }
                        disabled={
                          !onNavigate || index === visiblePath.length - 1
                        }
                        onClick={() => navigate(item.id)}
                      >
                        <span className="truncate">
                          {index === 0
                            ? (() => {
                                const root = roots.find(
                                  (candidate) => candidate.id === item.id
                                )
                                return root
                                  ? tf(rootNameMessage(root.scope))
                                  : item.name
                              })()
                            : item.name}
                        </span>
                      </Button>
                      {index < visiblePath.length - 1 && (
                        <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                      )}
                    </div>
                  ))
                ) : (
                  <span className="px-2 text-sm text-muted-foreground">
                    {tf("roots")}
                  </span>
                )}
              </nav>
              <AutoForm
                className="min-w-48 flex-1 md:grid-cols-[minmax(0,1fr)_9rem] [&>*]:col-span-1"
                hideLabel
                fields={{
                  query: {
                    name: tf("search"),
                    type: "text",
                    value: query,
                    disabled: !ready,
                    config: { placeholder: tf("search") },
                  },
                  kind: {
                    name: tf("type"),
                    type: "select",
                    value: kind,
                    disabled: !ready,
                    config: {
                      options: {
                        all: tf("allTypes"),
                        directory: tf("directory"),
                        file: tf("file"),
                      },
                    },
                  },
                }}
                onChange={(values) => {
                  if (values.query !== query)
                    setQuery(String(values.query ?? ""))
                  if (
                    values.kind !== kind &&
                    (values.kind === "all" ||
                      values.kind === "directory" ||
                      values.kind === "file")
                  )
                    setKind(values.kind)
                }}
              />
            </div>
            <div className="flex min-h-14 flex-wrap items-center justify-between gap-2 border-b px-3 py-1">
              <div className="flex min-h-11 min-w-0 flex-wrap items-center gap-1">
                {ready && visiblePath.length > 0 && (
                  <Tool
                    label={tf("selectionMode")}
                    active={selectionMode}
                    disabled={busy}
                    onClick={() => {
                      setSelectionMode((value) => {
                        if (value) {
                          setSelected(new Set())
                          setAnchor(null)
                        }
                        return !value
                      })
                    }}
                  >
                    <SquareCheckBig />
                  </Tool>
                )}
                {selectionMode && (
                  <>
                    <label className="flex min-h-11 items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={
                          rows.length > 0 &&
                          visibleSelected.length === rows.length
                        }
                        disabled={!rows.length || busy}
                        onChange={() =>
                          setSelected((previous) => {
                            const next = new Set(previous)
                            const all = visibleSelected.length === rows.length
                            for (const row of rows) {
                              if (all) next.delete(row.id)
                              else next.add(row.id)
                            }
                            return next
                          })
                        }
                      />
                      {tf("selectVisible")}
                    </label>
                    {selectedNodes.length > 0 && (
                      <>
                        <span className="mx-1 h-6 w-px bg-border" />
                        {recycleBin ? (
                          <Tool
                            label={tf("restoreSelected")}
                            disabled={busy || !onRestore}
                            onClick={() => void restoreNodes(selectedNodes)}
                          >
                            <RotateCcw />
                          </Tool>
                        ) : (
                          <>
                            <Tool
                              label={tf("downloadSelected")}
                              disabled={
                                busy ||
                                !selectedFiles.length ||
                                !onDownloadSelected
                              }
                              onClick={() =>
                                void onDownloadSelected?.(selectedFiles)
                              }
                            >
                              <Download />
                            </Tool>
                            <Tool
                              label={tf("copySelected")}
                              disabled={busy || !onCopy}
                              onClick={() => onCopy?.(selectedNodes)}
                            >
                              <Copy />
                            </Tool>
                            <Tool
                              label={tf("cutSelected")}
                              disabled={busy || !onCut}
                              onClick={() => onCut?.(selectedNodes)}
                            >
                              <Scissors />
                            </Tool>
                            <Tool
                              label={tf("trashSelected")}
                              disabled={busy || !onTrashSelected}
                              onClick={() =>
                                setEditor({
                                  kind: "trashSelection",
                                  nodes: selectedNodes,
                                })
                              }
                            >
                              <Trash2 />
                            </Tool>
                          </>
                        )}
                        <Tool
                          label={tf("clearSelection")}
                          disabled={busy}
                          onClick={() => {
                            setSelected(new Set())
                            setAnchor(null)
                          }}
                        >
                          <X />
                        </Tool>
                      </>
                    )}
                  </>
                )}
                {ready && !roots.length && onEnsureSystemRoot && (
                  <Button
                    className="min-h-11"
                    disabled={busy}
                    onClick={() => void onEnsureSystemRoot()}
                  >
                    {busy ? (
                      <LoaderCircle className="animate-spin" />
                    ) : (
                      <FolderPlus />
                    )}
                    {tf("createSystemRoot")}
                  </Button>
                )}
                {ready && writeLocation && (
                  <>
                    <Tool
                      label={tf("createDirectory")}
                      disabled={busy || !onCreateDirectory}
                      onClick={() => setEditor({ kind: "directory", name: "" })}
                    >
                      <FolderPlus />
                    </Tool>
                    <Tool
                      label={tf("upload")}
                      disabled={busy || !onUploadEntries}
                      onClick={() => uploadInput.current?.click()}
                    >
                      <Upload />
                    </Tool>
                    <Tool
                      label={
                        clipboard
                          ? tf("pasteCount", { count: clipboard.count })
                          : tf("paste")
                      }
                      disabled={busy || !onPaste}
                      onClick={() => void onPaste?.()}
                    >
                      <ClipboardPaste />
                    </Tool>
                    <input
                      ref={uploadInput}
                      className="hidden"
                      type="file"
                      multiple
                      aria-label={tf("upload")}
                      onChange={(event) => {
                        const files = Array.from(event.target.files ?? [])
                        event.target.value = ""
                        if (files.length)
                          void onUploadEntries?.(
                            files.map((file) => ({
                              kind: "file",
                              name: file.name,
                              file,
                            }))
                          )
                      }}
                    />
                  </>
                )}
                <Tool
                  label={tf("refresh")}
                  disabled={!onRefresh || status === "loading" || busy}
                  onClick={() => onRefresh?.()}
                >
                  <RefreshCw />
                </Tool>
              </div>
              <div className="ms-auto flex flex-wrap items-center justify-end gap-1">
                <div
                  role="group"
                  aria-label={tf("view")}
                  className="flex shrink-0"
                >
                  <Tool
                    label={tf("detailsView")}
                    active={view === "details"}
                    onClick={() => setView("details")}
                  >
                    <List />
                  </Tool>
                  <Tool
                    label={tf("smallView")}
                    active={view === "small"}
                    onClick={() => setView("small")}
                  >
                    <Grid2X2 className="size-4" />
                  </Tool>
                  <Tool
                    label={tf("largeView")}
                    active={view === "large"}
                    onClick={() => setView("large")}
                  >
                    <Grid2X2 />
                  </Tool>
                </div>
                <div className="w-32 shrink-0">
                  <span id="file-sort-label" className="sr-only">
                    {tf("sort")}
                  </span>
                  <SimpleSelect
                    aria-labelledby="file-sort-label"
                    triggerClassName="min-h-11"
                    value={sort}
                    options={{
                      name: tf("name"),
                      size: tf("size"),
                      updated: tf("updated"),
                    }}
                    onValueChange={(value) => {
                      if (
                        value === "name" ||
                        value === "size" ||
                        value === "updated"
                      )
                        setSort(value)
                    }}
                    disabled={!ready}
                  />
                </div>
                <Tool
                  label={tf(descending ? "ascending" : "descending")}
                  disabled={!ready}
                  onClick={() => setDescending((value) => !value)}
                >
                  {descending ? <ArrowDown /> : <ArrowUp />}
                </Tool>
              </div>
            </div>
            <ContextMenu>
              <ContextMenuTrigger
                aria-label={tf("currentDirectoryMenu")}
                className="flex min-h-64 flex-1 flex-col"
              >
                {!ready ? (
                  <div
                    role={status === "loading" ? "status" : "alert"}
                    className="flex min-h-64 flex-1 flex-col items-center justify-center gap-3 p-6 text-center"
                  >
                    <FolderOpen className="size-10 text-muted-foreground" />
                    <p className="text-sm">
                      {tf(
                        !validPage
                          ? "invalidPage"
                          : status === "ready"
                            ? "error"
                            : status
                      )}
                    </p>
                  </div>
                ) : !rows.length ? (
                  <div
                    role="status"
                    className="flex min-h-64 flex-1 flex-col items-center justify-center gap-3 p-6 text-center"
                  >
                    {recycleBin ? (
                      <Trash2 className="size-9 text-muted-foreground" />
                    ) : (
                      <Search className="size-9 text-muted-foreground" />
                    )}
                    <p className="text-sm">
                      {tf(
                        query || kind !== "all"
                          ? "noResults"
                          : recycleBin
                            ? "emptyRecycleBin"
                            : "empty"
                      )}
                    </p>
                  </div>
                ) : (
                  <div
                    role="group"
                    aria-label={tf("items")}
                    className={
                      view === "details"
                        ? "divide-y"
                        : `grid content-start gap-2 p-3 ${view === "large" ? "grid-cols-[repeat(auto-fill,minmax(132px,1fr))]" : "grid-cols-[repeat(auto-fill,minmax(190px,1fr))]"}`
                    }
                  >
                    {rows.map((node) => (
                      <ContextMenu key={node.id}>
                        <ContextMenuTrigger
                          data-file-id={node.id}
                          draggable={!recycleBin && !busy && Boolean(onMoveTo)}
                          onDragStart={(event) => startMove(event, node)}
                          onDragEnd={finishMove}
                          onDragOver={(event) => moveOver(event, node)}
                          onDragLeave={(event) => {
                            if (
                              !event.currentTarget.contains(
                                event.relatedTarget as Node
                              )
                            )
                              setMoveTargetID((current) =>
                                current === node.id ? null : current
                              )
                          }}
                          onDrop={(event) => void moveInto(event, node)}
                          className={`min-w-0 ${moveTargetID === node.id ? "bg-accent outline-2 -outline-offset-2 outline-primary" : selected.has(node.id) ? "bg-accent" : "hover:bg-muted/40"} ${view === "details" ? "flex min-h-14 items-center gap-2 px-3" : "relative flex min-h-20 items-center gap-2 rounded-md border p-2"} ${view === "large" ? "h-36 flex-col justify-center pt-8" : ""}`}
                        >
                          {selectionMode && (
                            <label
                              className={`flex size-11 shrink-0 items-center justify-center ${view === "large" ? "absolute start-0 top-0" : ""}`}
                            >
                              <input
                                type="checkbox"
                                className="size-4 accent-primary"
                                aria-label={tf("selectNamed", {
                                  name: node.name,
                                })}
                                checked={selected.has(node.id)}
                                disabled={busy}
                                onChange={(event) =>
                                  select(
                                    node.id,
                                    "shiftKey" in event.nativeEvent &&
                                      Boolean(event.nativeEvent.shiftKey)
                                  )
                                }
                              />
                            </label>
                          )}
                          <button
                            type="button"
                            data-file-open-icon
                            className="shrink-0 rounded-sm focus-visible:outline-2 focus-visible:outline-ring"
                            aria-label={tf(
                              node.kind === "directory" && !recycleBin
                                ? "selectNamed"
                                : "openNamed",
                              { name: node.name }
                            )}
                            title={node.name}
                            onClick={() => activate(node)}
                            onDoubleClick={() => {
                              if (node.kind === "directory") open(node)
                            }}
                            onKeyDown={(event) =>
                              openDirectoryWithKeyboard(event, node)
                            }
                          >
                            <FileVisual
                              node={node}
                              loadFile={!recycleBin ? onPreview : undefined}
                              size={view === "large" ? "large" : "compact"}
                            />
                          </button>
                          <button
                            type="button"
                            className="min-h-11 min-w-0 flex-1 text-start text-sm focus-visible:outline-2 focus-visible:outline-ring"
                            title={node.name}
                            onClick={() => activate(node)}
                            onDoubleClick={() => {
                              if (node.kind === "directory") open(node)
                            }}
                            onKeyDown={(event) =>
                              openDirectoryWithKeyboard(event, node)
                            }
                          >
                            <span
                              className={
                                view === "large"
                                  ? "line-clamp-2 text-center break-all"
                                  : "block truncate"
                              }
                            >
                              {node.name}
                            </span>
                            {recycleBin && (
                              <span
                                className={`mt-0.5 block text-xs text-muted-foreground ${view === "large" ? "line-clamp-2 text-center" : "truncate"}`}
                                title={
                                  restorePathLabel(node) ??
                                  tf("restoreLocationUnavailable")
                                }
                              >
                                {tf("restoreLocation")}:{" "}
                                {restorePathLabel(node) ??
                                  tf("restoreLocationUnavailable")}
                              </span>
                            )}
                            {view !== "details" && (
                              <span className="block text-xs text-muted-foreground">
                                {node.kind === "file"
                                  ? fileSizeLabel(node.size, locale)
                                  : tf("directory")}
                              </span>
                            )}
                          </button>
                          {view === "details" && (
                            <>
                              <span className="hidden w-24 shrink-0 text-end text-xs text-muted-foreground sm:block">
                                {node.kind === "file"
                                  ? fileSizeLabel(node.size, locale)
                                  : "\u2014"}
                              </span>
                              <span className="hidden w-28 shrink-0 text-end text-xs text-muted-foreground xl:block">
                                {date(node.updatedAt)}
                              </span>
                            </>
                          )}
                          {view === "details" && (
                            <DropdownMenu>
                              <DropdownMenuTrigger
                                aria-label={tf("actionsNamed", {
                                  name: node.name,
                                })}
                                disabled={busy}
                                className="flex size-11 shrink-0 items-center justify-center rounded-md hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
                              >
                                <MoreVertical className="size-4" />
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end" className="w-44">
                                {!recycleBin && node.kind === "file" && (
                                  <DropdownMenuItem
                                    disabled={!onPreview}
                                    onClick={() => setPreviewNode(node)}
                                  >
                                    <Eye className="mr-2 size-4" />
                                    {tf("preview")}
                                  </DropdownMenuItem>
                                )}
                                <DropdownMenuItem
                                  onClick={() => setDetailsID(node.id)}
                                >
                                  <Info className="mr-2 size-4" />
                                  {tf("properties")}
                                </DropdownMenuItem>
                                {recycleBin ? (
                                  <DropdownMenuItem
                                    disabled={!onRestore}
                                    onClick={() => void restoreNodes([node])}
                                  >
                                    <RotateCcw className="mr-2 size-4" />
                                    {tf("restore")}
                                  </DropdownMenuItem>
                                ) : (
                                  <>
                                    <DropdownMenuItem
                                      disabled={!onCopy}
                                      onClick={() =>
                                        onCopy?.(contextNodes(node))
                                      }
                                    >
                                      <Copy className="mr-2 size-4" />
                                      {tf("copy")}
                                    </DropdownMenuItem>
                                    <DropdownMenuItem
                                      disabled={!onCut}
                                      onClick={() =>
                                        onCut?.(contextNodes(node))
                                      }
                                    >
                                      <Scissors className="mr-2 size-4" />
                                      {tf("cut")}
                                    </DropdownMenuItem>
                                  </>
                                )}
                                {!recycleBin && node.kind === "file" && (
                                  <DropdownMenuItem
                                    disabled={!onDownload}
                                    onClick={() => void onDownload?.(node)}
                                  >
                                    <Download className="mr-2 size-4" />
                                    {tf("download")}
                                  </DropdownMenuItem>
                                )}
                                {!recycleBin && (
                                  <>
                                    <DropdownMenuItem
                                      disabled={!onRename}
                                      onClick={() =>
                                        setEditor({
                                          kind: "rename",
                                          node,
                                          name: node.name,
                                        })
                                      }
                                    >
                                      <Pencil className="mr-2 size-4" />
                                      {tf("rename")}
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem
                                      disabled={!onTrash}
                                      className="text-destructive focus:text-destructive"
                                      onClick={() =>
                                        setEditor({ kind: "trash", node })
                                      }
                                    >
                                      <Trash2 className="mr-2 size-4" />
                                      {tf("moveToTrash")}
                                    </DropdownMenuItem>
                                  </>
                                )}
                              </DropdownMenuContent>
                            </DropdownMenu>
                          )}
                        </ContextMenuTrigger>
                        <ContextMenuContent className="w-48">
                          {!recycleBin && node.kind === "directory" && (
                            <ContextMenuItem
                              disabled={!onNavigate}
                              onClick={() => navigate(node.id)}
                            >
                              <FolderOpen className="mr-2 size-4" />
                              {tf("open")}
                            </ContextMenuItem>
                          )}
                          {!recycleBin && node.kind === "file" && (
                            <ContextMenuItem
                              disabled={!onPreview}
                              onClick={() => setPreviewNode(node)}
                            >
                              <Eye className="mr-2 size-4" />
                              {tf("preview")}
                            </ContextMenuItem>
                          )}
                          {!recycleBin && (
                            <ContextMenuItem
                              disabled={!onOpenNewTab}
                              onClick={() => onOpenNewTab?.(node)}
                            >
                              <ExternalLink className="mr-2 size-4" />
                              {tf("openInNewTab")}
                            </ContextMenuItem>
                          )}
                          <ContextMenuItem
                            onClick={() => setDetailsID(node.id)}
                          >
                            <Info className="mr-2 size-4" />
                            {tf("properties")}
                          </ContextMenuItem>
                          {recycleBin ? (
                            <ContextMenuItem
                              disabled={!onRestore}
                              onClick={() => void restoreNodes([node])}
                            >
                              <RotateCcw className="mr-2 size-4" />
                              {tf("restore")}
                            </ContextMenuItem>
                          ) : (
                            <>
                              <ContextMenuItem
                                disabled={!onCopy}
                                onClick={() => onCopy?.(contextNodes(node))}
                              >
                                <Copy className="mr-2 size-4" />
                                {tf("copy")}
                              </ContextMenuItem>
                              <ContextMenuItem
                                disabled={!onCut}
                                onClick={() => onCut?.(contextNodes(node))}
                              >
                                <Scissors className="mr-2 size-4" />
                                {tf("cut")}
                              </ContextMenuItem>
                              {node.kind === "file" && (
                                <ContextMenuItem
                                  disabled={!onDownload}
                                  onClick={() => void onDownload?.(node)}
                                >
                                  <Download className="mr-2 size-4" />
                                  {tf("download")}
                                </ContextMenuItem>
                              )}
                              <ContextMenuItem
                                disabled={!onRename}
                                onClick={() =>
                                  setEditor({
                                    kind: "rename",
                                    node,
                                    name: node.name,
                                  })
                                }
                              >
                                <Pencil className="mr-2 size-4" />
                                {tf("rename")}
                              </ContextMenuItem>
                              <ContextMenuSeparator />
                              <ContextMenuItem
                                disabled={!onTrash}
                                className="text-destructive focus:text-destructive"
                                onClick={() =>
                                  setEditor({ kind: "trash", node })
                                }
                              >
                                <Trash2 className="mr-2 size-4" />
                                {tf("moveToTrash")}
                              </ContextMenuItem>
                            </>
                          )}
                        </ContextMenuContent>
                      </ContextMenu>
                    ))}
                  </div>
                )}
              </ContextMenuTrigger>
              <ContextMenuContent className="w-52">
                {!recycleBin && (
                  <>
                    <ContextMenuSub>
                      <ContextMenuSubTrigger
                        disabled={
                          busy ||
                          !ready ||
                          (!onCreateDirectory && !onCreateFile)
                        }
                      >
                        <FilePlus2 className="mr-2 size-4" />
                        {tf("createNew")}
                      </ContextMenuSubTrigger>
                      <ContextMenuContent className="w-52">
                        <ContextMenuItem
                          disabled={!onCreateDirectory}
                          onClick={() =>
                            setEditor({ kind: "directory", name: "" })
                          }
                        >
                          <FolderPlus className="mr-2 size-4" />
                          {tf("createDirectory")}
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem
                          disabled={!onCreateFile}
                          onClick={() =>
                            setEditor({
                              kind: "file",
                              name: "untitled.txt",
                              extension: ".txt",
                              mimeType: "text/plain",
                              title: "createTextFile",
                            })
                          }
                        >
                          <FileText className="mr-2 size-4" />
                          {tf("createTextFile")}
                        </ContextMenuItem>
                        <ContextMenuItem
                          disabled={!onCreateFile}
                          onClick={() =>
                            setEditor({
                              kind: "file",
                              name: "untitled.md",
                              extension: ".md",
                              mimeType: "text/markdown",
                              title: "createMarkdownFile",
                            })
                          }
                        >
                          <FileCode2 className="mr-2 size-4" />
                          {tf("createMarkdownFile")}
                        </ContextMenuItem>
                      </ContextMenuContent>
                    </ContextMenuSub>
                    <ContextMenuItem
                      disabled={busy || !onUploadEntries || !ready}
                      onClick={() => uploadInput.current?.click()}
                    >
                      <Upload className="mr-2 size-4" />
                      {tf("upload")}
                    </ContextMenuItem>
                    <ContextMenuSeparator />
                    <ContextMenuItem
                      disabled={busy || !onPaste}
                      onClick={() => void onPaste?.()}
                    >
                      <ClipboardPaste className="mr-2 size-4" />
                      {clipboard
                        ? tf("pasteCount", { count: clipboard.count })
                        : tf("paste")}
                    </ContextMenuItem>
                    <ContextMenuSeparator />
                  </>
                )}
                <ContextMenuItem
                  disabled={busy || !onRefresh}
                  onClick={() => onRefresh?.()}
                >
                  <RefreshCw className="mr-2 size-4" />
                  {tf("refresh")}
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
            <footer className="mt-auto flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
              <span role="status">
                {tf("counts", {
                  count: rows.length,
                  selected: ready ? selected.size : 0,
                })}
              </span>
              <div className="flex gap-2">
                {onPreviousPage && (
                  <Button
                    variant="outline"
                    className="min-h-11"
                    disabled={!ready}
                    onClick={onPreviousPage}
                  >
                    {tf("previousPage")}
                  </Button>
                )}
                {onNextPage && (
                  <Button
                    variant="outline"
                    className="min-h-11"
                    disabled={!ready}
                    onClick={onNextPage}
                  >
                    {tf("nextPage")}
                  </Button>
                )}
              </div>
            </footer>
          </div>
          {details && ready && wide && (
            <aside
              aria-label={tf("properties")}
              className="hidden w-64 shrink-0 border-s xl:block"
            >
              {properties}
            </aside>
          )}
        </div>
        <Sheet
          open={Boolean(details) && ready && !wide}
          onOpenChange={(open) => {
            if (!open) setDetailsID(null)
          }}
        >
          <SheetContent
            side="right"
            initialFocus={propertyTitle}
            className="w-full overflow-y-auto sm:max-w-sm xl:hidden [&>button]:hidden"
          >
            <SheetTitle className="sr-only">{tf("properties")}</SheetTitle>
            {properties}
          </SheetContent>
        </Sheet>
        {previewNode && onPreview && (
          <FilePreviewDialog
            key={`${previewNode.id}:${previewNode.revision}`}
            node={previewNode}
            loadFile={onPreview}
            onDownload={onDownload ? () => onDownload(previewNode) : undefined}
            onClose={() => {
              setPreviewNode(null)
              onClosePreview?.()
            }}
          />
        )}
        <Dialog
          open={
            editor?.kind === "directory" ||
            editor?.kind === "file" ||
            editor?.kind === "rename" ||
            editor?.kind === "restoreRename"
          }
          onOpenChange={(open) => {
            if (!open && !busy) setEditor(null)
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {tf(
                  editor?.kind === "restoreRename"
                    ? "restoreWithNewName"
                    : editor?.kind === "file"
                      ? editor.title
                      : editor?.kind === "rename"
                        ? "rename"
                        : "createDirectory"
                )}
              </DialogTitle>
              <DialogDescription>
                {editor?.kind === "file"
                  ? tf("createFileDescription", {
                      extension: editor.extension,
                    })
                  : tf(
                      editor?.kind === "rename"
                        ? "renameDescription"
                        : editor?.kind === "restoreRename"
                          ? "restoreRenameDescription"
                          : "createDirectoryDescription"
                    )}
              </DialogDescription>
            </DialogHeader>
            <AutoForm
              key={
                editor?.kind +
                ":" +
                (editor && "node" in editor ? editor.node.id : "")
              }
              id="file-editor-form"
              className="md:grid-cols-1 [&>*]:col-span-1"
              onSubmit={submitEditor}
              fields={{
                name: {
                  name: tf("name"),
                  type: "text",
                  required: true,
                  value: editor && "name" in editor ? editor.name : "",
                  config: {
                    id: "file-editor-name",
                    autoFocus: true,
                    minLength: 1,
                    maxLength: 255,
                  },
                },
              }}
              onChange={(values) =>
                setEditor((current) =>
                  current && "name" in current && current.name !== values.name
                    ? { ...current, name: String(values.name ?? "") }
                    : current
                )
              }
            />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setEditor(null)}
              >
                {tf("cancel")}
              </Button>
              <Button
                type="submit"
                form="file-editor-form"
                disabled={
                  busy || !editor || !("name" in editor) || !editor.name.trim()
                }
              >
                {busy && <LoaderCircle className="animate-spin" />}
                {tf("save")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        <Dialog
          open={editor?.kind === "trash" || editor?.kind === "trashSelection"}
          onOpenChange={(open) => {
            if (!open && !busy) setEditor(null)
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{tf("moveToTrash")}</DialogTitle>
              <DialogDescription>
                {editor?.kind === "trashSelection"
                  ? tf("trashSelectionDescription", {
                      count: editor.nodes.length,
                    })
                  : tf("trashDescription", {
                      name: editor?.kind === "trash" ? editor.node.name : "",
                    })}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setEditor(null)}
              >
                {tf("cancel")}
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={busy}
                onClick={() => void confirmTrash()}
              >
                {busy && <LoaderCircle className="animate-spin" />}
                {tf("confirmTrash")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </section>
    </TooltipProvider>
  )
}
