import type { FileChildrenPage, FilePage } from "./iam-api"

export type FileBrowserNode = {
  id: string
  name: string
  kind: "directory" | "file"
  size?: number
  updatedAt?: string
  detectedType?: string
  extension?: string
  version?: string
  revision: number
  restorePath?: FileBrowserLocation[]
}

export type FileBrowserLocation = { id: string; name: string }
export type FileBrowserRootScope =
  "account" | "personal" | "tenant" | "entity" | "department"
export type FileBrowserRoot = FileBrowserLocation & {
  scope: FileBrowserRootScope
  ownerID: string
  tenantID?: string
  ownerName?: string
}
export type FileBrowserSort = "name" | "size" | "updated"

export const FILE_PREVIEW_MAX_BYTES = 32 * 1024 * 1024
export const FILE_THUMBNAIL_MAX_BYTES = 8 * 1024 * 1024

export function parentFileBrowserDirectoryID(
  path: readonly FileBrowserLocation[]
): string | undefined {
  return path.at(-2)?.id
}

export type FilePresentationCategory =
  | "pdf"
  | "document"
  | "spreadsheet"
  | "presentation"
  | "image"
  | "video"
  | "audio"
  | "archive"
  | "text"
  | "code"
  | "email"
  | "book"
  | "database"
  | "drawing"
  | "cad"
  | "model3d"
  | "map"
  | "font"
  | "unknown"

export type FilePreviewKind =
  | "image"
  | "text"
  | "pdf"
  | "media"
  | "office"
  | "unsupported"

export type FilePresentation = {
  category: FilePresentationCategory
  preview: FilePreviewKind
  badge?: string
  editable: boolean
}

const extensionCategories: Readonly<Record<string, FilePresentationCategory>> = {
  pdf: "pdf",
  doc: "document",
  docx: "document",
  docm: "document",
  dot: "document",
  dotx: "document",
  odt: "document",
  fodt: "document",
  rtf: "document",
  wps: "document",
  xls: "spreadsheet",
  xlsx: "spreadsheet",
  xlsm: "spreadsheet",
  xlsb: "spreadsheet",
  ods: "spreadsheet",
  fods: "spreadsheet",
  numbers: "spreadsheet",
  et: "spreadsheet",
  csv: "spreadsheet",
  tsv: "spreadsheet",
  ppt: "presentation",
  pptx: "presentation",
  pptm: "presentation",
  pps: "presentation",
  ppsx: "presentation",
  odp: "presentation",
  fodp: "presentation",
  key: "presentation",
  dps: "presentation",
  jpg: "image",
  jpeg: "image",
  png: "image",
  gif: "image",
  webp: "image",
  avif: "image",
  svg: "image",
  bmp: "image",
  ico: "image",
  tif: "image",
  tiff: "image",
  apng: "image",
  heic: "image",
  heif: "image",
  mp4: "video",
  webm: "video",
  mov: "video",
  m4v: "video",
  avi: "video",
  mkv: "video",
  flv: "video",
  wmv: "video",
  m3u8: "video",
  mp3: "audio",
  wav: "audio",
  ogg: "audio",
  oga: "audio",
  aac: "audio",
  m4a: "audio",
  flac: "audio",
  opus: "audio",
  mid: "audio",
  midi: "audio",
  zip: "archive",
  rar: "archive",
  "7z": "archive",
  tar: "archive",
  gz: "archive",
  tgz: "archive",
  bz2: "archive",
  xz: "archive",
  eml: "email",
  msg: "email",
  mbox: "email",
  epub: "book",
  xps: "book",
  oxps: "book",
  sqlite: "database",
  sqlite3: "database",
  db: "database",
  parquet: "database",
  avro: "database",
  drawio: "drawing",
  dio: "drawing",
  excalidraw: "drawing",
  tldraw: "drawing",
  xmind: "drawing",
  dwg: "cad",
  dxf: "cad",
  dwf: "cad",
  step: "cad",
  stp: "cad",
  iges: "cad",
  igs: "cad",
  ifc: "cad",
  skp: "cad",
  sldprt: "cad",
  sldasm: "cad",
  gltf: "model3d",
  glb: "model3d",
  obj: "model3d",
  stl: "model3d",
  fbx: "model3d",
  dae: "model3d",
  ply: "model3d",
  "3mf": "model3d",
  usd: "model3d",
  usdz: "model3d",
  geojson: "map",
  topojson: "map",
  kml: "map",
  kmz: "map",
  gpx: "map",
  shp: "map",
  ttf: "font",
  otf: "font",
  woff: "font",
  woff2: "font",
}

const textExtensions = new Set([
  "txt",
  "log",
  "md",
  "markdown",
  "mmd",
  "json",
  "jsonc",
  "json5",
  "jsonl",
  "geojson",
  "topojson",
  "xml",
  "yaml",
  "yml",
  "toml",
  "ini",
  "conf",
  "config",
  "properties",
  "env",
  "csv",
  "tsv",
  "js",
  "jsx",
  "mjs",
  "cjs",
  "ts",
  "tsx",
  "vue",
  "svelte",
  "html",
  "htm",
  "css",
  "scss",
  "less",
  "py",
  "go",
  "rs",
  "java",
  "kt",
  "kts",
  "swift",
  "rb",
  "php",
  "c",
  "cpp",
  "h",
  "hpp",
  "cs",
  "sql",
  "sh",
  "bash",
  "zsh",
  "fish",
  "ps1",
  "bat",
  "cmd",
  "dockerfile",
  "graphql",
  "gql",
  "proto",
  "tf",
  "tfvars",
  "hcl",
  "tex",
  "bib",
  "diff",
  "patch",
])

const codeExtensions = new Set(
  [...textExtensions].filter(
    (extension) =>
      ![
        "txt",
        "log",
        "md",
        "markdown",
        "mmd",
        "csv",
        "tsv",
        "conf",
        "config",
        "properties",
      ].includes(extension)
  )
)

function normalizedExtension(node: Pick<FileBrowserNode, "name" | "extension">) {
  const supplied = node.extension?.trim().replace(/^\.+/u, "").toLowerCase()
  if (supplied) return supplied
  const base = node.name.split(/[\\/]/u).at(-1) ?? node.name
  const index = base.lastIndexOf(".")
  return index > 0 ? base.slice(index + 1).toLowerCase() : base.toLowerCase()
}

function mimeCategory(mime: string): FilePresentationCategory | undefined {
  if (!mime || mime === "application/octet-stream") return undefined
  if (mime === "application/pdf") return "pdf"
  if (mime.startsWith("image/")) return "image"
  if (mime.startsWith("video/")) return "video"
  if (mime.startsWith("audio/")) return "audio"
  if (mime.startsWith("font/")) return "font"
  if (mime.startsWith("message/")) return "email"
  if (mime === "text/plain" || mime === "text/markdown") return "text"
  if (mime === "text/csv" || mime === "text/tab-separated-values")
    return "spreadsheet"
  if (mime.includes("wordprocessingml") || mime.includes("msword") || mime.includes("opendocument.text") || mime === "application/rtf") return "document"
  if (mime.includes("spreadsheetml") || mime.includes("ms-excel") || mime.includes("opendocument.spreadsheet")) return "spreadsheet"
  if (mime.includes("presentationml") || mime.includes("ms-powerpoint") || mime.includes("opendocument.presentation")) return "presentation"
  if (mime.includes("zip") || mime.includes("rar") || mime.includes("7z") || mime.includes("tar") || mime.includes("gzip") || mime.includes("bzip") || mime.includes("xz")) return "archive"
  if (mime.includes("epub") || mime.includes("xps")) return "book"
  if (mime.includes("sqlite") || mime.includes("parquet") || mime.includes("avro")) return "database"
  if (mime.includes("geo+") || mime.includes("kml") || mime.includes("gpx") || mime.includes("shapefile")) return "map"
  if (mime.startsWith("model/")) return "model3d"
  if (
    mime.startsWith("text/") ||
    mime.includes("json") ||
    mime.includes("xml") ||
    mime.includes("yaml") ||
    mime.includes("javascript") ||
    mime.includes("typescript") ||
    mime.includes("sql")
  )
    return "code"
  return undefined
}

function isGenericZipMIME(mime: string) {
  return mime === "application/zip" || mime === "application/x-zip-compressed"
}

export function classifyFilePresentation(
  node: Pick<FileBrowserNode, "name" | "extension" | "detectedType">
): FilePresentation {
  const extension = normalizedExtension(node)
  const mime = node.detectedType?.split(";", 1)[0]?.trim().toLowerCase() ?? ""
  const extensionCategory = extensionCategories[extension]
  const category =
    (isGenericZipMIME(mime) && extensionCategory !== "archive"
      ? extensionCategory
      : mimeCategory(mime)) ??
    extensionCategory ??
    (codeExtensions.has(extension)
      ? "code"
      : textExtensions.has(extension)
        ? "text"
        : "unknown")
  const preview: FilePreviewKind =
    category === "image"
      ? "image"
      : category === "pdf"
        ? "pdf"
        : category === "video" || category === "audio"
          ? "media"
          : (category === "document" &&
                (extension === "docx" || mime.includes("wordprocessingml"))) ||
              (category === "presentation" &&
                (extension === "pptx" || mime.includes("presentationml"))) ||
              (category === "spreadsheet" &&
                (extension === "xlsx" || mime.includes("spreadsheetml")))
            ? "office"
          : textExtensions.has(extension) || mime.startsWith("text/") || category === "code"
            ? "text"
            : "unsupported"
  const badge = extension && extension.length <= 5 ? extension.toUpperCase() : undefined
  return {
    category,
    preview,
    badge,
    editable: preview === "text",
  }
}

export type DroppedEntry =
  | { kind: "file"; name: string; file: File }
  | { kind: "directory"; name: string; children: DroppedEntry[] }

export type DroppedFileSystemEntry = {
  name: string
  isFile: boolean
  isDirectory: boolean
  file?: (success: (file: File) => void, failure?: () => void) => void
  createReader?: () => {
    readEntries: (
      success: (entries: DroppedFileSystemEntry[]) => void,
      failure?: () => void
    ) => void
  }
}

export type DroppedDataTransferItem = {
  kind: string
  getAsFile: () => File | null
  webkitGetAsEntry?: () => DroppedFileSystemEntry | null
}

export type DropLimits = { maxDepth?: number; maxEntries?: number }

const fileID = /^[1-9][0-9]*$/

export async function parseDroppedEntries(
  items: readonly DroppedDataTransferItem[],
  fallbackFiles: readonly File[],
  limits: DropLimits = {}
): Promise<DroppedEntry[]> {
  const maxDepth = limits.maxDepth ?? 64
  const maxEntries = limits.maxEntries ?? 1000
  let count = 0

  const take = () => {
    count += 1
    if (count > maxEntries) throw new Error("drop_too_many_entries")
  }
  const readFile = (entry: DroppedFileSystemEntry) =>
    new Promise<File>((resolve, reject) => {
      if (!entry.file) return reject(new Error("drop_read_failed"))
      entry.file(resolve, () => reject(new Error("drop_read_failed")))
    })
  const readDirectory = (entry: DroppedFileSystemEntry) =>
    new Promise<DroppedFileSystemEntry[]>((resolve, reject) => {
      if (!entry.createReader) return reject(new Error("drop_read_failed"))
      const reader = entry.createReader()
      const entries: DroppedFileSystemEntry[] = []
      const next = () =>
        reader.readEntries(
          (batch) => {
            if (!batch.length) return resolve(entries)
            entries.push(...batch)
            next()
          },
          () => reject(new Error("drop_read_failed"))
        )
      next()
    })
  const readEntry = async (
    entry: DroppedFileSystemEntry,
    depth: number
  ): Promise<DroppedEntry> => {
    if (depth > maxDepth) throw new Error("drop_too_deep")
    take()
    if (entry.isFile) {
      const file = await readFile(entry)
      return { kind: "file", name: entry.name || file.name, file }
    }
    if (!entry.isDirectory) throw new Error("drop_read_failed")
    const children = await readDirectory(entry)
    return {
      kind: "directory",
      name: entry.name,
      children: await Promise.all(
        children.map((child) => readEntry(child, depth + 1))
      ),
    }
  }

  const entries: DroppedEntry[] = []
  for (const item of items) {
    if (item.kind !== "file") continue
    const entry = item.webkitGetAsEntry?.()
    if (entry) entries.push(await readEntry(entry, 1))
    else {
      const file = item.getAsFile()
      if (file) {
        take()
        entries.push({ kind: "file", name: file.name, file })
      }
    }
  }
  if (entries.length) return entries
  for (const file of fallbackFiles) {
    take()
    entries.push({ kind: "file", name: file.name, file })
  }
  return entries
}

export async function materializeDroppedEntries(
  parentID: string,
  entries: readonly DroppedEntry[],
  createDirectory: (parentID: string, name: string) => Promise<string>,
  uploadFile: (parentID: string, file: File) => Promise<void>
): Promise<void> {
  for (const entry of entries) {
    if (entry.kind === "file") {
      await uploadFile(parentID, entry.file)
      continue
    }
    const directoryID = await createDirectory(parentID, entry.name)
    if (!directoryID) throw new Error("invalid_response")
    await materializeDroppedEntries(
      directoryID,
      entry.children,
      createDirectory,
      uploadFile
    )
  }
}

export function fileBrowserRoots(
  page: FilePage,
  ownerNames: Readonly<Record<string, string>> = {}
): FileBrowserRoot[] {
  const items = page.items ?? []
  if (items.length > 100) throw new Error("invalid_response")
  const seen = new Set<string>()
  return items.map((item) => {
    const identifiers = [
      item.tenant_id,
      item.entity_id,
      item.department_id,
      item.principal_id,
    ].filter((value): value is string => value !== undefined)
    if (
      !item.id ||
      !fileID.test(item.id) ||
      !item.name ||
      item.kind !== "directory" ||
      identifiers.some((value) => !fileID.test(value)) ||
      seen.has(item.id)
    )
      throw new Error("invalid_response")
    let ownerID: string | undefined
    switch (item.scope) {
      case "account":
        if (item.tenant_id || item.entity_id || item.department_id)
          throw new Error("invalid_response")
        ownerID = item.principal_id
        break
      case "personal":
        if (!item.tenant_id || item.entity_id || item.department_id)
          throw new Error("invalid_response")
        ownerID = item.principal_id
        break
      case "tenant":
        if (
          !item.tenant_id ||
          item.entity_id ||
          item.department_id ||
          item.principal_id
        )
          throw new Error("invalid_response")
        ownerID = item.tenant_id
        break
      case "entity":
        if (!item.tenant_id || item.department_id || item.principal_id)
          throw new Error("invalid_response")
        ownerID = item.entity_id
        break
      case "department":
        if (!item.tenant_id || item.entity_id || item.principal_id)
          throw new Error("invalid_response")
        ownerID = item.department_id
        break
      default:
        throw new Error("invalid_response")
    }
    if (!ownerID || !fileID.test(ownerID)) throw new Error("invalid_response")
    seen.add(item.id)
    return {
      id: item.id,
      name: item.name,
      scope: item.scope,
      ownerID,
      tenantID: item.tenant_id,
      ...(ownerNames[ownerID] ? { ownerName: ownerNames[ownerID] } : {}),
    }
  })
}

export function fileBrowserChildren(page: FileChildrenPage): {
  path: FileBrowserLocation[]
  nodes: FileBrowserNode[]
  nextAfter: string
} {
  const items = page.items ?? []
  const path = page.path ?? []
  if (items.length > 100) throw new Error("invalid_response")
  const seen = new Set<string>()
  const nodes = items.map((item) => {
    const restorePath = item.restore_path ?? undefined
    if (
      !item.id ||
      !fileID.test(item.id) ||
      !item.name ||
      (item.kind !== "directory" && item.kind !== "file") ||
      !Number.isSafeInteger(item.revision) ||
      item.revision < 1 ||
      (item.version !== undefined &&
        (!Number.isSafeInteger(item.version) || item.version < 1)) ||
      !Number.isFinite(Date.parse(item.updated_at)) ||
      (item.size !== undefined &&
        (!Number.isSafeInteger(item.size) || item.size < 0)) ||
      (restorePath !== undefined &&
        (restorePath.length === 0 ||
          restorePath.length > 65 ||
          new Set(restorePath.map((segment) => segment.id)).size !==
            restorePath.length ||
          restorePath.some(
            (segment) =>
              !segment.id || !fileID.test(segment.id) || !segment.name
          ))) ||
      seen.has(item.id)
    )
      throw new Error("invalid_response")
    seen.add(item.id)
    const kind = item.kind as FileBrowserNode["kind"]
    return {
      id: item.id,
      name: item.name,
      kind,
      size: item.size,
      updatedAt: item.updated_at,
      detectedType: item.detected_type,
      extension: item.extension,
      version: item.version === undefined ? undefined : String(item.version),
      revision: item.revision,
      restorePath: restorePath?.map((segment) => ({
        id: segment.id!,
        name: segment.name,
      })),
    }
  })
  const locations = path.map((item) => {
    if (!item.id || !fileID.test(item.id) || !item.name)
      throw new Error("invalid_response")
    return { id: item.id, name: item.name }
  })
  if (page.next_after && !fileID.test(page.next_after))
    throw new Error("invalid_response")
  return { path: locations, nodes, nextAfter: page.next_after ?? "" }
}

export function fileBrowserEntries(page: FilePage): {
  nodes: FileBrowserNode[]
  nextAfter: string
} {
  const result = fileBrowserChildren({
    items: page.items,
    path: [],
    next_after: page.next_after,
  })
  return { nodes: result.nodes, nextAfter: result.nextAfter }
}

export function visibleFileNodes(
  nodes: readonly FileBrowserNode[],
  query: string,
  kind: "all" | FileBrowserNode["kind"],
  sort: FileBrowserSort,
  descending: boolean,
  locale: string
): FileBrowserNode[] {
  const search = query.normalize("NFC").toLocaleLowerCase(locale).trim()
  const collator = new Intl.Collator(locale, {
    numeric: true,
    sensitivity: "base",
  })
  return nodes
    .filter(
      (node) =>
        (kind === "all" || node.kind === kind) &&
        node.name.normalize("NFC").toLocaleLowerCase(locale).includes(search)
    )
    .sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === "directory" ? -1 : 1
      const order =
        sort === "size"
          ? (a.size ?? -1) - (b.size ?? -1)
          : sort === "updated"
            ? (Date.parse(a.updatedAt ?? "") || 0) -
              (Date.parse(b.updatedAt ?? "") || 0)
            : collator.compare(a.name, b.name)
      return (descending ? -order : order) || a.id.localeCompare(b.id)
    })
}

export function selectFileRange(
  selected: ReadonlySet<string>,
  rows: readonly FileBrowserNode[],
  id: string,
  anchor: string | null,
  range: boolean
): Set<string> {
  const end = rows.findIndex((node) => node.id === id)
  if (end < 0) return new Set(selected)
  const start = range ? rows.findIndex((node) => node.id === anchor) : -1
  const next = new Set(selected)
  if (start >= 0) {
    for (const node of rows.slice(
      Math.min(start, end),
      Math.max(start, end) + 1
    ))
      next.add(node.id)
  } else if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

export function fileSizeLabel(
  size: number | undefined,
  locale: string
): string {
  if (size === undefined || !Number.isFinite(size) || size < 0) return "\u2014"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  const unit = Math.max(
    0,
    Math.min(
      size === 0 ? 0 : Math.floor(Math.log(size) / Math.log(1024)),
      units.length - 1
    )
  )
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(size / 1024 ** unit)} ${units[unit]}`
}
