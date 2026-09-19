import { useEffect, useRef, useState } from "react"
import {
  BookOpen,
  Box,
  Database,
  DraftingCompass,
  File,
  FileArchive,
  FileAudio,
  FileCode2,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileType2,
  FileVideo,
  Folder,
  Mail,
  Map,
  Presentation,
  Shapes,
  type LucideIcon,
} from "lucide-react"
import {
  FILE_THUMBNAIL_MAX_BYTES,
  classifyFilePresentation,
  type FileBrowserNode,
  type FilePresentationCategory,
} from "../lib/file-browser"

export type FileContentLoader = (
  node: FileBrowserNode,
  maxBytes: number,
  signal: AbortSignal
) => Promise<Blob>

const categoryIcons: Record<
  FilePresentationCategory,
  { icon: LucideIcon; color: string }
> = {
  pdf: { icon: FileText, color: "text-red-600 dark:text-red-400" },
  document: { icon: FileText, color: "text-blue-600 dark:text-blue-400" },
  spreadsheet: { icon: FileSpreadsheet, color: "text-emerald-600 dark:text-emerald-400" },
  presentation: { icon: Presentation, color: "text-orange-600 dark:text-orange-400" },
  image: { icon: FileImage, color: "text-fuchsia-600 dark:text-fuchsia-400" },
  video: { icon: FileVideo, color: "text-rose-600 dark:text-rose-400" },
  audio: { icon: FileAudio, color: "text-cyan-600 dark:text-cyan-400" },
  archive: { icon: FileArchive, color: "text-amber-700 dark:text-amber-400" },
  text: { icon: FileText, color: "text-slate-700 dark:text-slate-300" },
  code: { icon: FileCode2, color: "text-teal-600 dark:text-teal-400" },
  email: { icon: Mail, color: "text-indigo-600 dark:text-indigo-400" },
  book: { icon: BookOpen, color: "text-sky-700 dark:text-sky-400" },
  database: { icon: Database, color: "text-lime-700 dark:text-lime-400" },
  drawing: { icon: Shapes, color: "text-pink-600 dark:text-pink-400" },
  cad: { icon: DraftingCompass, color: "text-yellow-700 dark:text-yellow-400" },
  model3d: { icon: Box, color: "text-violet-600 dark:text-violet-400" },
  map: { icon: Map, color: "text-green-700 dark:text-green-400" },
  font: { icon: FileType2, color: "text-slate-700 dark:text-slate-300" },
  unknown: { icon: File, color: "text-muted-foreground" },
}

export function FileTypeIcon({
  node,
  className = "size-5",
  showBadge = false,
}: {
  node: FileBrowserNode
  className?: string
  showBadge?: boolean
}) {
  if (node.kind === "directory")
    return <Folder aria-hidden="true" className={`shrink-0 text-amber-600 ${className}`} />

  const presentation = classifyFilePresentation(node)
  const entry = categoryIcons[presentation.category]
  const Icon = entry.icon
  return (
    <span
      aria-hidden="true"
      data-file-category={presentation.category}
      className={`relative inline-flex shrink-0 items-center justify-center ${className}`}
    >
      <Icon className={`${entry.color} size-full`} />
      {showBadge && presentation.badge && (
        <span className="absolute -end-2 -bottom-1 max-w-10 truncate bg-background px-0.5 text-[8px] leading-3 font-bold text-foreground ring-1 ring-border">
          {presentation.badge}
        </span>
      )}
    </span>
  )
}

export function FileVisual({
  node,
  loadFile,
  size = "compact",
}: {
  node: FileBrowserNode
  loadFile?: FileContentLoader
  size?: "compact" | "large"
}) {
  const presentation = classifyFilePresentation(node)
  const eligible = node.kind === "file" && presentation.category === "image"
  const host = useRef<HTMLSpanElement>(null)
  const [visible, setVisible] = useState(
    () => typeof window !== "undefined" && !("IntersectionObserver" in window)
  )
  const key = `${node.id}:${node.revision}`
  const [thumbnailState, setThumbnailState] = useState({
    key: "",
    url: "",
    failed: false,
  })
  const currentState =
    thumbnailState.key === key
      ? thumbnailState
      : { key, url: "", failed: false }

  useEffect(() => {
    const element = host.current
    if (!eligible || !loadFile || !element) return
    if (!("IntersectionObserver" in window)) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true)
          observer.disconnect()
        }
      },
      { rootMargin: "160px" }
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [eligible, loadFile, node.id, node.revision])

  useEffect(() => {
    if (!eligible || !loadFile || !visible) return
    const controller = new AbortController()
    let active = true
    let objectURL = ""
    void loadFile(node, FILE_THUMBNAIL_MAX_BYTES, controller.signal)
      .then((blob) => {
        if (!active) return
        objectURL = URL.createObjectURL(blob)
        setThumbnailState({ key, url: objectURL, failed: false })
      })
      .catch(() => {
        if (active && !controller.signal.aborted)
          setThumbnailState({ key, url: "", failed: true })
      })
    return () => {
      active = false
      controller.abort()
      if (objectURL) URL.revokeObjectURL(objectURL)
    }
  }, [eligible, key, loadFile, node, visible])

  const thumbnailReady =
    eligible && Boolean(loadFile) && currentState.url && !currentState.failed
  return (
    <span
      ref={host}
      aria-hidden="true"
      data-file-category={presentation.category}
      data-file-thumbnail={
        eligible && loadFile
          ? thumbnailReady
            ? "ready"
            : currentState.failed
              ? "failed"
              : "loading"
          : undefined
      }
      className={`relative inline-flex shrink-0 items-center justify-center overflow-hidden rounded-sm bg-muted/30 ${size === "large" ? "size-16" : "size-9"}`}
    >
      {thumbnailReady ? (
        <img
          src={currentState.url}
          alt=""
          className="size-full object-contain"
          loading="lazy"
          onError={() =>
            setThumbnailState({ key, url: currentState.url, failed: true })
          }
        />
      ) : (
        <FileTypeIcon
          node={node}
          className="size-full"
          showBadge={size === "large"}
        />
      )}
    </span>
  )
}
