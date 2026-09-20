import { useCallback, useEffect, useRef, useState } from "react"
import { useOutletContext, useSearchParams } from "react-router"
import {
  FileBrowser,
  type FileBrowserProps,
} from "../../components/file-browser"
import {
  fileBrowserChildren,
  fileBrowserEntries,
  fileBrowserRoots,
  materializeDroppedEntries,
  parentFileBrowserDirectoryID,
  type FileBrowserLocation,
  type FileBrowserNode,
  type FileBrowserRoot,
} from "../../lib/file-browser"
import { ApiError, clientErrorMessage, iamApi } from "../../lib/iam-api"

type BrowserState = {
  requestKey: string
  roots: FileBrowserRoot[]
  path: FileBrowserLocation[]
  nodes: FileBrowserNode[]
  nextAfter: string
  status: FileBrowserProps["status"]
}

type FileClipboard = {
  action: "copy" | "move"
  nodes: FileBrowserNode[]
  sourceParentID: string
  scopeKey: string
}

const emptyState: BrowserState = {
  requestKey: "",
  roots: [],
  path: [],
  nodes: [],
  nextAfter: "",
  status: "loading",
}

function failureStatus(cause: unknown): FileBrowserProps["status"] {
  if (cause instanceof ApiError && cause.status === 403) return "forbidden"
  if (cause instanceof ApiError && cause.status === 503) return "unavailable"
  return "error"
}

function actionMessage(cause: unknown): string {
  if (cause instanceof ApiError) return cause.message
  if (cause instanceof Error && cause.message)
    return clientErrorMessage(cause.message)
  return clientErrorMessage("invalid_response")
}

export default function FilesPage({ account = false }: { account?: boolean }) {
  const outlet = useOutletContext<{
    tenantID: string
    tenantName?: string
    entityID: string
    entityNames?: Record<string, string>
  } | null>()
  const tenantID = account ? "" : (outlet?.tenantID ?? "")
  const tenantName = account ? "" : (outlet?.tenantName ?? "")
  const entityID = account ? "" : (outlet?.entityID ?? "")
  const entityNames = account ? undefined : outlet?.entityNames
  const fileEntityID = account ? null : entityID || undefined
  const [searchParams, setSearchParams] = useSearchParams()
  const [state, setState] = useState<BrowserState>(emptyState)
  const [recycleBin, setRecycleBin] = useState(false)
  const [locationScope, setLocationScope] = useState(
    account ? "account" : `${tenantID}:${entityID}`
  )
  const currentNodeID = useRef<string | null>(null)
  const [recycleReturn, setRecycleReturn] = useState<{
    scope: string
    id: string
  } | null>(null)
  const [pageHistory, setPageHistory] = useState<string[]>([])
  const [directoryBack, setDirectoryBack] = useState<string[]>([])
  const [directoryForward, setDirectoryForward] = useState<string[]>([])
  const [reload, setReload] = useState(0)
  const [busy, setBusy] = useState(false)
  const [clipboard, setClipboard] = useState<FileClipboard | null>(null)
  const [actionError, setActionError] = useState<{
    scope: string
    message: string
  } | null>(null)
  const generation = useRef(0)
  const scopeKey = account ? "account" : `${tenantID}:${entityID}`
  const queryID = /^[1-9][0-9]{0,18}$/u
  const requestedDirectoryID = searchParams.get("directory")
  const requestedPreviewID = searchParams.get("preview")
  const selectedNodeID =
    locationScope === scopeKey &&
    requestedDirectoryID &&
    queryID.test(requestedDirectoryID)
      ? requestedDirectoryID
      : null
  const previewID =
    requestedPreviewID && queryID.test(requestedPreviewID)
      ? requestedPreviewID
      : undefined
  const pageAfter =
    locationScope === scopeKey && queryID.test(searchParams.get("after") ?? "")
      ? (searchParams.get("after") ?? "")
      : ""
  const visiblePageHistory = locationScope === scopeKey ? pageHistory : []
  const visibleDirectoryBack = locationScope === scopeKey ? directoryBack : []
  const visibleDirectoryForward =
    locationScope === scopeKey ? directoryForward : []
  const requestKey = [
    scopeKey,
    recycleBin ? "trashed" : "active",
    selectedNodeID ?? "",
    pageAfter,
    reload,
  ].join(":")
  const visibleState: BrowserState =
    !account && !tenantID
      ? { ...emptyState, status: "selectEntity" }
      : state.requestKey === requestKey
        ? state
        : emptyState
  const currentDirectoryID = visibleState.path.at(-1)?.id ?? null
  const directoryBackTarget =
    visibleDirectoryBack.at(-1) ??
    parentFileBrowserDirectoryID(visibleState.path)

  useEffect(() => {
    const request = ++generation.current
    let active = true
    if (!account && !tenantID) return
    void iamApi
      .files(fileEntityID)
      .then(async (rootPage) => {
        const departmentIDs = account
          ? []
          : [
              ...new Set(
                (rootPage.items ?? []).flatMap((root) =>
                  root.department_id ? [root.department_id] : []
                )
              ),
            ]
        const departments = (
          await Promise.all(
            departmentIDs.map((id) =>
              iamApi.department(id).catch(() => null)
            )
          )
        ).filter((department) => department !== null)
        const roots = fileBrowserRoots(
          rootPage,
          {
            ...(tenantID && tenantName ? { [tenantID]: tenantName } : {}),
            ...(entityNames ?? {}),
            ...Object.fromEntries(
              departments.map((department) => [department.id, department.name])
            ),
          }
        )
        if (recycleBin) {
          const trash = fileBrowserEntries(
            await iamApi.trashedFiles(fileEntityID, pageAfter)
          )
          if (!active || request !== generation.current) return
          currentNodeID.current = null
          setState({
            requestKey,
            roots,
            path: [],
            ...trash,
            status: "ready",
          })
          return
        }
        const selectedID = selectedNodeID ?? roots[0]?.id ?? null
        if (!selectedID) {
          if (active && request === generation.current) {
            currentNodeID.current = null
            setState({
              ...emptyState,
              requestKey,
              roots,
              status: "ready",
            })
          }
          return
        }
        const children = fileBrowserChildren(
          await iamApi.fileItems(fileEntityID, selectedID, pageAfter)
        )
        if (!active || request !== generation.current) return
        currentNodeID.current = selectedID
        setState({ requestKey, roots, ...children, status: "ready" })
      })
      .catch((cause: unknown) => {
        if (!active || request !== generation.current) return
        currentNodeID.current = null
        setState({
          ...emptyState,
          requestKey,
          status: failureStatus(cause),
        })
      })
    return () => {
      active = false
    }
  }, [
    account,
    tenantID,
    tenantName,
    entityID,
    entityNames,
    fileEntityID,
    selectedNodeID,
    recycleBin,
    pageAfter,
    reload,
    requestKey,
  ])

  const refresh = () => setReload((value) => value + 1)
  const setDirectoryLocation = (id: string | null, replace = false) =>
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      if (id) next.set("directory", id)
      else next.delete("directory")
      next.delete("preview")
      next.delete("after")
      return next
    }, { replace })
  const setPageLocation = (value: string) =>
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      if (value) next.set("after", value)
      else next.delete("after")
      next.delete("preview")
      return next
    })
  const navigate = (nextNodeID: string) => {
    const current = currentNodeID.current
    setDirectoryBack((values) =>
      current && current !== nextNodeID
        ? locationScope === scopeKey
          ? [...values, current]
          : [current]
        : locationScope === scopeKey
          ? values
          : []
    )
    setDirectoryForward([])
    setRecycleBin(false)
    setLocationScope(scopeKey)
    setPageHistory([])
    setActionError(null)
    setDirectoryLocation(nextNodeID)
  }
  const run = async (operation: () => Promise<void>): Promise<boolean> => {
    if (busy) return false
    setBusy(true)
    setActionError(null)
    try {
      await operation()
      return true
    } catch (cause) {
      setActionError({ scope: scopeKey, message: actionMessage(cause) })
      return false
    } finally {
      setBusy(false)
    }
  }
  const parentID = () => currentNodeID.current
  const setFileClipboard = (
    action: FileClipboard["action"],
    nodes: readonly FileBrowserNode[]
  ) => {
    const sourceParentID = parentID()
    if (!sourceParentID || !nodes.length) return
    setClipboard({ action, nodes: [...nodes], sourceParentID, scopeKey })
  }
  const download = async (node: FileBrowserNode) => {
    const blob = await iamApi.downloadFile(fileEntityID, node.id)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = node.name
    anchor.click()
    URL.revokeObjectURL(url)
  }
  const openFileInNewTab = (node: FileBrowserNode) => {
    window.open(
      iamApi.fileContentURL(fileEntityID, node.id),
      "_blank",
      "noopener,noreferrer"
    )
  }
  const preview = useCallback(
    (node: FileBrowserNode, maxBytes: number, signal: AbortSignal) =>
      iamApi.previewFile(fileEntityID, node.id, maxBytes, signal),
    [fileEntityID]
  )
  const restore = async (
    node: FileBrowserNode,
    name?: string
  ): Promise<"restored" | "name-conflict" | "failed"> => {
    if (busy) return "failed"
    setBusy(true)
    setActionError(null)
    try {
      await iamApi.restoreFileEntry(fileEntityID, node.id, node.revision, name)
      refresh()
      return "restored"
    } catch (cause) {
      if (
        cause instanceof ApiError &&
        cause.code === "file_restore_name_conflict"
      )
        return "name-conflict"
      setActionError({ scope: scopeKey, message: actionMessage(cause) })
      return "failed"
    } finally {
      setBusy(false)
    }
  }

  return (
    <FileBrowser
      roots={visibleState.roots}
      path={visibleState.path}
      nodes={visibleState.nodes}
      status={visibleState.status}
      busy={busy}
      actionError={actionError?.scope === scopeKey ? actionError.message : ""}
      clipboard={
        clipboard?.scopeKey === scopeKey
          ? { action: clipboard.action, count: clipboard.nodes.length }
          : undefined
      }
      initialPreviewID={previewID}
      onClosePreview={() =>
        setSearchParams((current) => {
          const next = new URLSearchParams(current)
          next.delete("preview")
          return next
        }, { replace: true })
      }
      onOpenNewTab={(node) => {
        if (node.kind === "directory") {
          const target = new URL(window.location.href)
          target.searchParams.delete("preview")
          target.searchParams.set("directory", node.id)
          target.searchParams.delete("after")
          window.open(target, "_blank", "noopener,noreferrer")
          return
        }
        openFileInNewTab(node)
      }}
      recycleBin={recycleBin}
      onOpenRecycleBin={
        visibleState.status === "ready"
          ? () => {
              const current = currentNodeID.current
              if (current) setRecycleReturn({ scope: scopeKey, id: current })
              setRecycleBin(true)
              setLocationScope(scopeKey)
              setDirectoryLocation(null)
              setPageHistory([])
              setActionError(null)
            }
          : undefined
      }
      onNavigate={visibleState.status === "ready" ? navigate : undefined}
      onBack={
        recycleBin && recycleReturn?.scope === scopeKey
          ? () => {
              setRecycleBin(false)
              setPageHistory([])
              setDirectoryLocation(recycleReturn.id)
            }
          : directoryBackTarget
            ? () => {
                const current = currentNodeID.current
                if (!current) return
                setDirectoryBack((values) => values.slice(0, -1))
                setDirectoryForward((values) => [...values, current])
                setPageHistory([])
                setActionError(null)
                setDirectoryLocation(directoryBackTarget)
              }
            : undefined
      }
      onForward={
        visibleDirectoryForward.length
          ? () => {
              const target = visibleDirectoryForward.at(-1)
              const current = currentNodeID.current
              if (!target || !current) return
              setDirectoryForward((values) => values.slice(0, -1))
              setDirectoryBack((values) => [...values, current])
              setPageHistory([])
              setActionError(null)
              setDirectoryLocation(target)
            }
          : undefined
      }
      onRefresh={account || entityID ? refresh : undefined}
      onEnsurePersonalRoot={
        !recycleBin && (account || entityID)
          ? () =>
              run(async () => {
                const root = account
                  ? await iamApi.createAccountFileRoot()
                  : await iamApi.createPersonalFileRoot(entityID)
                if (!root.id) throw new Error("invalid_response")
                setLocationScope(scopeKey)
                setDirectoryLocation(root.id)
                refresh()
              })
          : undefined
      }
      onCreateDirectory={
        !recycleBin
          ? (name) =>
              run(async () => {
                const id = parentID()
                if (!id) throw new Error("invalid_response")
                await iamApi.createDirectory(fileEntityID, id, name)
                refresh()
              })
          : undefined
      }
      onCreateFile={
        !recycleBin
          ? (name, mimeType) =>
              run(async () => {
                const id = parentID()
                if (!id) throw new Error("invalid_response")
                await iamApi.uploadFile(
                  fileEntityID,
                  id,
                  new File([], name, { type: mimeType })
                )
                refresh()
              })
          : undefined
      }
      onUploadEntries={
        !recycleBin
          ? (entries) =>
              run(async () => {
                const id = parentID()
                if (!id) throw new Error("invalid_response")
                try {
                  await materializeDroppedEntries(
                    id,
                    entries,
                    async (targetID, name) => {
                      const directory = await iamApi.createDirectory(
                        fileEntityID,
                        targetID,
                        name
                      )
                      if (!directory.id) throw new Error("invalid_response")
                      return directory.id
                    },
                    async (targetID, file) => {
                      await iamApi.uploadFile(fileEntityID, targetID, file)
                    }
                  )
                } finally {
                  refresh()
                }
              })
          : undefined
      }
      onDownload={
        !recycleBin
          ? (node) =>
              run(async () => {
                await download(node)
              })
          : undefined
      }
      onPreview={
        !recycleBin ? preview : undefined
      }
      onDownloadSelected={
        !recycleBin
          ? (nodes) =>
              run(async () => {
                for (const node of nodes) await download(node)
              })
          : undefined
      }
      onRename={
        !recycleBin
          ? (node, name) =>
              run(async () => {
                await iamApi.renameFileEntry(
                  fileEntityID,
                  node.id,
                  node.revision,
                  name
                )
                refresh()
              })
          : undefined
      }
      onTrash={
        !recycleBin
          ? (node) =>
              run(async () => {
                await iamApi.trashFileEntry(
                  fileEntityID,
                  node.id,
                  node.revision
                )
                refresh()
              })
          : undefined
      }
      onTrashSelected={
        !recycleBin
          ? (nodes) =>
              run(async () => {
                try {
                  for (const node of nodes)
                    await iamApi.trashFileEntry(
                      fileEntityID,
                      node.id,
                      node.revision
                    )
                } finally {
                  refresh()
                }
              })
          : undefined
      }
      onRestore={recycleBin ? restore : undefined}
      onCopy={
        !recycleBin ? (nodes) => setFileClipboard("copy", nodes) : undefined
      }
      onCut={
        !recycleBin ? (nodes) => setFileClipboard("move", nodes) : undefined
      }
      onPaste={
        !recycleBin &&
        clipboard?.scopeKey === scopeKey &&
        !(
          clipboard.action === "move" &&
          clipboard.sourceParentID === currentDirectoryID
        )
          ? () =>
              run(async () => {
                const destinationID = parentID()
                if (!destinationID || !clipboard.nodes.length)
                  throw new Error("invalid_response")
                await iamApi.createFileOperation(fileEntityID, {
                  action: clipboard.action,
                  destination_id: destinationID,
                  sources: clipboard.nodes.map((node) => node.id),
                })
                if (clipboard.action === "move") setClipboard(null)
                refresh()
              })
          : undefined
      }
      onMoveTo={
        !recycleBin
          ? (nodes, destination) =>
              run(async () => {
                if (!nodes.length || destination.kind !== "directory")
                  throw new Error("invalid_response")
                await iamApi.createFileOperation(fileEntityID, {
                  action: "move",
                  destination_id: destination.id,
                  sources: nodes.map((node) => node.id),
                })
                refresh()
              })
          : undefined
      }
      onPreviousPage={
        visiblePageHistory.length
          ? () => {
              const previous = visiblePageHistory.at(-1) ?? ""
              setPageHistory((values) => values.slice(0, -1))
              setPageLocation(previous)
            }
          : undefined
      }
      onNextPage={
        visibleState.nextAfter
          ? () => {
              setLocationScope(scopeKey)
              setPageHistory((values) => [...values, pageAfter])
              setPageLocation(visibleState.nextAfter)
            }
          : undefined
      }
    />
  )
}
