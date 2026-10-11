import { useCallback, useEffect, useRef, useState } from "react"
import { useOutletContext, useSearchParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { getAccountView } from "../../lib/account-view"
import { useAllowed } from "../../lib/permissions"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  FileBrowser,
  type FileBrowserProps,
} from "../../components/file-browser"
import {
  fileBrowserChildren,
  fileBrowserEntries,
  fileBrowserRoots,
  mergeFileCollectionPages,
  materializeDroppedEntries,
  parentFileBrowserDirectoryID,
  type FileBrowserLocation,
  type FileBrowserNode,
  type FileBrowserRoot,
} from "../../lib/file-browser"
import {
  ApiError,
  businessCode,
  clientErrorMessage,
  iamApi,
  type FileOperationInput,
  type FilePage,
} from "../../lib/iam-api"
import {
  confirmedFileOperation,
  executeFileOperation,
  fileOperationIdentity,
  type PendingFileOperation,
} from "../../lib/file-operation"

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

export function SystemFilesPage() {
  return <FilesPage system />
}

export default function FilesPage({
  account = false,
  system = false,
}: {
  account?: boolean
  system?: boolean
}) {
  const tf = useTranslations("files")
  const allowed = useAllowed()
  const canViewDepartments = allowed("dept_view")
  const writable = getAccountView()?.mode !== "read_only"
  const outlet = useOutletContext<{
    tenantID: string
    tenantName?: string
    entityID: string
    entityNames?: Record<string, string>
    permissions?: ReadonlySet<string> | null
  } | null>()
  const fileAllowed = (verb: string) =>
    !system ||
    (["read", "download", "list", "traverse"].includes(verb)
      ? outlet?.permissions?.has(`system_file_${verb}`) === true
      : allowed(`system_file_${verb}`))
  const tenantID = account || system ? "" : (outlet?.tenantID ?? "")
  const tenantName = account || system ? "" : (outlet?.tenantName ?? "")
  const entityID = account || system ? "" : (outlet?.entityID ?? "")
  const entityNames = account || system ? undefined : outlet?.entityNames
  const fileEntityID = account ? null : entityID || undefined
  const [searchParams, setSearchParams] = useSearchParams()
  const [state, setState] = useState<BrowserState>(emptyState)
  const [recycleBin, setRecycleBin] = useState(false)
  const [locationScope, setLocationScope] = useState(
    system ? "system" : account ? "account" : `${tenantID}:${entityID}`
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
  const [pendingOperation, setPendingOperation] =
    useState<PendingFileOperation | null>(null)
  const [actionError, setActionError] = useState<{
    scope: string
    message: string
  } | null>(null)
  const generation = useRef(0)
  const operationAttempt = useRef<{
    identity: string
    idempotencyKey: string
  } | null>(null)
  const scopeKey = system
    ? "system"
    : account
      ? "account"
      : `${tenantID}:${entityID}`
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
    !account && !system && !tenantID
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
    if (!account && !system && !tenantID) return
    const loadCollections = async () => {
      if (account || system)
        return {
          rootPages: [await iamApi.files(fileEntityID, "", system)],
          names: {},
          trash: recycleBin
            ? await iamApi.trashedFiles(fileEntityID, pageAfter, system)
            : null,
        }
      const [entities, departments] = await Promise.all([
        entityID ? Promise.resolve([]) : iamApi.memberEntities("file_view"),
        canViewDepartments ? iamApi.departments() : Promise.resolve([]),
      ])
      const visibleDepartments = departments.filter(
        (department) => !entityID || department.entity_id === entityID
      )
      const entityIDs = entityID
        ? [entityID]
        : entities.map((entity) => entity.id)
      const collections = [
        ...(!entityID
          ? [
              (after: string, state: "active" | "trashed") =>
                iamApi.files(undefined, after, false, state),
            ]
          : []),
        ...entityIDs.map(
          (id) => (after: string, state: "active" | "trashed") =>
            iamApi.files(id, after, false, state)
        ),
        ...visibleDepartments.map(
          (department) => (after: string, state: "active" | "trashed") =>
            iamApi
              .departmentFiles(department.id, after, state)
              .catch((cause: unknown): FilePage => {
                // Department names may be visible without file access to that owner.
                if (cause instanceof ApiError && cause.status === 403)
                  return { items: [] }
                throw cause
              })
        ),
      ]
      const rootPages = await Promise.all(
        collections.map(async (load) => {
          const pages: FilePage[] = []
          let after = ""
          do {
            const page = await load(after, "active")
            pages.push(page)
            if (
              page.next_after &&
              BigInt(page.next_after) <= BigInt(after || "0")
            )
              throw new Error("invalid_response")
            after = page.next_after ?? ""
          } while (after)
          return pages
        })
      )
      return {
        rootPages: rootPages.flat(),
        names: Object.fromEntries(
          [...entities, ...visibleDepartments].map((owner) => [
            owner.id,
            owner.name,
          ])
        ),
        trash: recycleBin
          ? mergeFileCollectionPages(
              await Promise.all(
                collections.map((load) => load(pageAfter, "trashed"))
              )
            )
          : null,
      }
    }
    void loadCollections()
      .then(async ({ rootPages, names, trash: trashPage }) => {
        const roots = rootPages.flatMap((rootPage) =>
          fileBrowserRoots(rootPage, {
            ...(tenantID && tenantName ? { [tenantID]: tenantName } : {}),
            ...(entityNames ?? {}),
            ...names,
          })
        )
        if (recycleBin) {
          const trash = fileBrowserEntries(trashPage!)
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
    system,
    canViewDepartments,
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
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (id) next.set("directory", id)
        else next.delete("directory")
        next.delete("preview")
        next.delete("after")
        return next
      },
      { replace }
    )
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
  const writeParentID = async () => {
    const current = parentID()
    if (current) return current
    if (
      !account ||
      !writable ||
      recycleBin ||
      visibleState.status !== "ready" ||
      visibleState.roots.length
    )
      throw new Error("invalid_response")
    const root = await iamApi.createAccountFileRoot()
    if (!root.id) throw new Error("invalid_response")
    currentNodeID.current = root.id
    setLocationScope(scopeKey)
    setDirectoryLocation(root.id)
    return root.id
  }
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
    if (getAccountView()) {
      const tab = window.open("", "_blank")
      if (!tab) return
      tab.opener = null
      void iamApi.downloadFile(fileEntityID, node.id).then(
        (blob) => {
          const url = URL.createObjectURL(blob)
          tab.location.replace(url)
          window.setTimeout(() => URL.revokeObjectURL(url), 60_000)
        },
        () => tab.close()
      )
      return
    }
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
        cause.businessCode === businessCode.fileRestoreNameConflict
      )
        return "name-conflict"
      setActionError({ scope: scopeKey, message: actionMessage(cause) })
      return "failed"
    } finally {
      setBusy(false)
    }
  }

  const performFileOperation = async (
    input: FileOperationInput,
    suppliedIdempotencyKey?: string
  ): Promise<boolean> => {
    if (busy) return false
    const identity = fileOperationIdentity(input)
    const idempotencyKey =
      suppliedIdempotencyKey ??
      (operationAttempt.current?.identity === identity
        ? operationAttempt.current.idempotencyKey
        : crypto.randomUUID())
    operationAttempt.current = { identity, idempotencyKey }
    setBusy(true)
    setActionError(null)
    try {
      const result = await executeFileOperation(
        (request, key) =>
          iamApi.createFileOperation(fileEntityID, request, key, system),
        input,
        idempotencyKey
      )
      if (result.kind === "conflict") {
        setPendingOperation(result.pending)
        return false
      }
      if (result.kind === "error") {
        if (result.definitive) operationAttempt.current = null
        setActionError({
          scope: scopeKey,
          message: actionMessage(result.cause),
        })
        return false
      }
      operationAttempt.current = null
      setPendingOperation(null)
      if (input.action === "move") setClipboard(null)
      refresh()
      return true
    } finally {
      setBusy(false)
    }
  }

  const confirmFileOperation = () => {
    if (!pendingOperation) return
    const input = confirmedFileOperation(pendingOperation)
    if (!input) return
    void performFileOperation(input, pendingOperation.idempotencyKey)
  }

  return (
    <>
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
          setSearchParams(
            (current) => {
              const next = new URLSearchParams(current)
              next.delete("preview")
              return next
            },
            { replace: true }
          )
        }
        onOpenNewTab={
          fileAllowed("read")
            ? (node) => {
                if (node.kind === "directory") {
                  const target = new URL(window.location.href)
                  target.searchParams.delete("preview")
                  target.searchParams.set("directory", node.id)
                  target.searchParams.delete("after")
                  window.open(target, "_blank", "noopener,noreferrer")
                  return
                }
                openFileInNewTab(node)
              }
            : undefined
        }
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
        onRefresh={refresh}
        personal={account}
        onEnsureSystemRoot={
          !recycleBin && system && writable && allowed("system_file_create")
            ? () =>
                run(async () => {
                  const root = await iamApi.createSystemFileRoot()
                  if (!root.id) throw new Error("invalid_response")
                  setLocationScope(scopeKey)
                  setDirectoryLocation(root.id)
                  refresh()
                })
            : undefined
        }
        onCreateDirectory={
          !recycleBin && writable && fileAllowed("create")
            ? (name) =>
                run(async () => {
                  const id = await writeParentID()
                  await iamApi.createDirectory(fileEntityID, id, name, system)
                  refresh()
                })
            : undefined
        }
        onCreateFile={
          !recycleBin && writable && fileAllowed("create")
            ? (name, mimeType) =>
                run(async () => {
                  const id = await writeParentID()
                  await iamApi.uploadFile(
                    fileEntityID,
                    id,
                    new File([], name, { type: mimeType }),
                    system
                  )
                  refresh()
                })
            : undefined
        }
        onUploadEntries={
          !recycleBin && writable && fileAllowed("create")
            ? (entries) =>
                run(async () => {
                  const id = await writeParentID()
                  try {
                    await materializeDroppedEntries(
                      id,
                      entries,
                      async (targetID, name) => {
                        const directory = await iamApi.createDirectory(
                          fileEntityID,
                          targetID,
                          name,
                          system
                        )
                        if (!directory.id) throw new Error("invalid_response")
                        return directory.id
                      },
                      async (targetID, file) => {
                        await iamApi.uploadFile(
                          fileEntityID,
                          targetID,
                          file,
                          system
                        )
                      }
                    )
                  } finally {
                    refresh()
                  }
                })
            : undefined
        }
        onDownload={
          !recycleBin && fileAllowed("download")
            ? (node) =>
                run(async () => {
                  await download(node)
                })
            : undefined
        }
        onPreview={!recycleBin && fileAllowed("read") ? preview : undefined}
        onDownloadSelected={
          !recycleBin && fileAllowed("download")
            ? (nodes) =>
                run(async () => {
                  for (const node of nodes) await download(node)
                })
            : undefined
        }
        onRename={
          !recycleBin && writable && fileAllowed("rename")
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
          !recycleBin && writable && fileAllowed("delete")
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
          !recycleBin && writable && fileAllowed("delete")
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
        onRestore={
          recycleBin && writable && fileAllowed("restore") ? restore : undefined
        }
        onCopy={
          !recycleBin &&
          writable &&
          fileAllowed("read") &&
          fileAllowed("create")
            ? (nodes) => setFileClipboard("copy", nodes)
            : undefined
        }
        onCut={
          !recycleBin && writable && fileAllowed("move")
            ? (nodes) => setFileClipboard("move", nodes)
            : undefined
        }
        onPaste={
          !recycleBin &&
          writable &&
          clipboard?.scopeKey === scopeKey &&
          fileAllowed(clipboard.action === "move" ? "move" : "create") &&
          !(
            clipboard.action === "move" &&
            clipboard.sourceParentID === currentDirectoryID
          )
            ? () => {
                const destinationID = parentID()
                if (!destinationID || !clipboard.nodes.length)
                  return Promise.resolve(false)
                return performFileOperation({
                  action: clipboard.action,
                  destination_id: destinationID,
                  sources: clipboard.nodes.map((node) => node.id),
                })
              }
            : undefined
        }
        onMoveTo={
          !recycleBin && writable && fileAllowed("move")
            ? (nodes, destination) => {
                if (!nodes.length || destination.kind !== "directory")
                  return Promise.resolve(false)
                return performFileOperation({
                  action: "move",
                  destination_id: destination.id,
                  sources: nodes.map((node) => node.id),
                })
              }
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
      <Dialog
        open={Boolean(pendingOperation)}
        onOpenChange={(open) => {
          if (!open && !busy) setPendingOperation(null)
        }}
      >
        <DialogContent className="max-h-[85svh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{tf("operationConflicts")}</DialogTitle>
            <DialogDescription>
              {tf("operationConflictsDescription")}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            {pendingOperation?.frontier.conflicts.map((conflict, index) => (
              <div
                key={conflict.key}
                className="grid gap-2 border-b pb-4 last:border-b-0 last:pb-0"
              >
                <div className="min-w-0 text-sm font-medium">
                  {tf("conflictItem", {
                    index: index + 1,
                    source: conflict.source.name,
                    target: conflict.target.name,
                  })}
                </div>
                <fieldset className="grid gap-2">
                  <legend className="text-sm text-muted-foreground">
                    {tf("conflictAction")}
                  </legend>
                  <div className="flex flex-wrap gap-2">
                    {conflict.policies.map((policy) => {
                      const selected =
                        pendingOperation.decisions[conflict.key] === policy
                      return (
                        <Button
                          key={policy}
                          type="button"
                          disabled={busy}
                          variant={selected ? "default" : "outline"}
                          className="h-auto min-h-11 whitespace-normal"
                          aria-pressed={selected}
                          onClick={() =>
                            setPendingOperation((current) =>
                              current
                                ? {
                                    ...current,
                                    decisions: {
                                      ...current.decisions,
                                      [conflict.key]: policy,
                                    },
                                  }
                                : current
                            )
                          }
                        >
                          {tf(`conflictPolicy_${policy}`)}
                        </Button>
                      )
                    })}
                  </div>
                </fieldset>
              </div>
            ))}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => setPendingOperation(null)}
            >
              {tf("cancel")}
            </Button>
            <Button
              type="button"
              disabled={
                busy ||
                !pendingOperation ||
                pendingOperation.frontier.conflicts.some(
                  (conflict) => !pendingOperation.decisions[conflict.key]
                )
              }
              onClick={confirmFileOperation}
            >
              {tf("continueOperation")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
