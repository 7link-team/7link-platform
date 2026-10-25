// Isolated browser fixture. Never imported by the production router.
import { useState } from "react"
import { createRoot } from "react-dom/client"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import {
  FileBrowser,
  type FileBrowserProps,
} from "../../src/components/file-browser"
import type { DroppedEntry, FileBrowserNode } from "../../src/lib/file-browser"
import {
  fileBrowserViewState,
  type FileBrowserViewState,
} from "../../src/lib/file-browser"
import en from "../../src/messages/files/en-US.json"
import zh from "../../src/messages/files/zh-CN.json"
import ms from "../../src/messages/files/ms-MY.json"
import "../../src/index.css"

const nodes: FileBrowserNode[] = [
  { id: "folder", name: "Documents", kind: "directory", revision: 1 },
  {
    id: "a",
    name: "Annual report.pdf",
    kind: "file",
    size: 2048,
    detectedType: "application/pdf",
    extension: "pdf",
    version: "2",
    updatedAt: "2026-09-18T10:00:00Z",
    revision: 1,
  },
  { id: "b", name: "Budget.xlsx", kind: "file", size: 8192, revision: 1 },
  { id: "c", name: "Notes.txt", kind: "file", size: 0, revision: 1 },
  {
    id: "long",
    name: "Very-long-file-name-without-spaces-for-responsive-layout-validation-2026.pdf",
    kind: "file",
    size: 2 ** 31,
    revision: 1,
  },
]

export default function Fixture() {
  const [location, setLocation] = useState("root")
  const [status, setStatus] = useState<FileBrowserProps["status"]>("ready")
  const [page, setPage] = useState(1)
  const [revokedMetadata, setRevokedMetadata] = useState(false)
  const [operation, setOperation] = useState("")
  const [canForward, setCanForward] = useState(false)
  const controlled = new URLSearchParams(window.location.search).has(
    "controlled"
  )
  const [viewState, setViewState] = useState(() =>
    fileBrowserViewState(new URLSearchParams())
  )
  const [viewChanges, setViewChanges] = useState(0)
  const updateView = (next: FileBrowserViewState) => {
    setViewChanges((count) => count + 1)
    setViewState(next)
  }
  const [clipboard, setClipboard] = useState<{
    action: "copy" | "move"
    count: number
  }>()
  return (
    <main className="mx-auto max-w-[1440px] p-4 sm:p-6">
      <div className="mb-3 flex flex-wrap gap-3 text-xs">
        <button onClick={() => setStatus("forbidden")}>
          Fixture: forbidden
        </button>
        <button onClick={() => setStatus("ready")}>Fixture: ready</button>
        <button onClick={() => setStatus("loading")}>Fixture: loading</button>
        <button onClick={() => setStatus("error")}>Fixture: error</button>
        <button onClick={() => setRevokedMetadata(true)}>
          Fixture: revoke metadata
        </button>
        <output data-testid="location">{location}</output>
        <output data-testid="operation">{operation}</output>
        {controlled && (
          <>
            <button
              onClick={() =>
                setViewState((current) => ({
                  ...current,
                  query: "Notes",
                  kind: "file",
                }))
              }
            >
              Fixture: restore filters
            </button>
            <button
              onClick={() =>
                setViewState(fileBrowserViewState(new URLSearchParams()))
              }
            >
              Fixture: reset filters
            </button>
            <output data-testid="view-changes">{viewChanges}</output>
          </>
        )}
      </div>
      <FileBrowser
        viewState={controlled ? viewState : undefined}
        onViewStateChange={controlled ? updateView : undefined}
        roots={[
          {
            id: "root",
            name: "Team files",
            scope: "entity",
            ownerID: "80",
            tenantID: "90",
          },
        ]}
        path={[
          { id: "root", name: "Team files" },
          ...(location === "folder"
            ? [{ id: "folder", name: "Documents" }]
            : []),
        ]}
        nodes={
          location === "folder"
            ? []
            : page === 2
              ? [
                  {
                    id: "next",
                    name: "Next page.txt",
                    kind: "file",
                    revision: 1,
                  },
                ]
              : nodes.map((node) =>
                  revokedMetadata
                    ? { ...node, detectedType: undefined, version: undefined }
                    : node
                )
        }
        status={status}
        onNavigate={(id) => {
          setLocation(id)
          setPage(1)
          setCanForward(false)
        }}
        onBack={
          location === "folder"
            ? () => {
                setLocation("root")
                setCanForward(true)
              }
            : undefined
        }
        onForward={
          location === "root" && canForward
            ? () => {
                setLocation("folder")
                setCanForward(false)
              }
            : undefined
        }
        onRefresh={() => setStatus("ready")}
        onCreateDirectory={async () => true}
        onUploadEntries={async (entries: readonly DroppedEntry[]) => {
          setOperation(
            `uploaded:${entries.map((entry) => entry.name).join(",")}`
          )
          return true
        }}
        onDownload={async (node) => {
          setOperation(`downloaded:${node.name}`)
          return true
        }}
        onDownloadSelected={async (selected) => {
          setOperation(
            `downloaded:${selected.map((node) => node.name).join(",")}`
          )
          return true
        }}
        onRename={async () => true}
        onTrash={async () => true}
        onTrashSelected={async (selected) => {
          setOperation(`trashed:${selected.map((node) => node.name).join(",")}`)
          return true
        }}
        clipboard={clipboard}
        onCopy={(selected) => {
          setClipboard({ action: "copy", count: selected.length })
          setOperation(`copied:${selected.map((node) => node.name).join(",")}`)
        }}
        onCut={(selected) => {
          setClipboard({ action: "move", count: selected.length })
          setOperation(`cut:${selected.map((node) => node.name).join(",")}`)
        }}
        onPaste={
          clipboard
            ? async () => {
                setOperation(`pasted:${clipboard.count}`)
                if (clipboard.action === "move") setClipboard(undefined)
                return true
              }
            : undefined
        }
        onNextPage={page === 1 ? () => setPage(2) : undefined}
        onPreviousPage={page === 2 ? () => setPage(1) : undefined}
      />
    </main>
  )
}

const locale = new URLSearchParams(location.search).get("locale") ?? "en-US"
const messages = locale === "zh-CN" ? zh : locale === "ms-MY" ? ms : en
createRoot(document.getElementById("root")!).render(
  <IntlAppProvider initialLocale={locale} initialMessages={{ files: messages }}>
    <Fixture />
  </IntlAppProvider>
)
