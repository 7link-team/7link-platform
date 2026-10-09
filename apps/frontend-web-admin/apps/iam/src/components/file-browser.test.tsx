import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { FileBrowser, type FileBrowserProps } from "./file-browser"
import { FileVisual } from "./file-type-icon"
import en from "../messages/files/en-US.json"
import zh from "../messages/files/zh-CN.json"
import ms from "../messages/files/ms-MY.json"

function render(props: Partial<FileBrowserProps> = {}) {
  return renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ files: en }}>
      <FileBrowser roots={[]} path={[]} nodes={[]} status="ready" {...props} />
    </IntlAppProvider>
  )
}

describe("FileBrowser", () => {
  it("separates unavailable, forbidden, loading, error and empty states", () => {
    for (const status of [
      "unavailable",
      "forbidden",
      "loading",
      "selectEntity",
      "error",
    ] as const) {
      expect(render({ status })).toContain(en[status])
      expect(render({ status })).not.toContain(en.empty)
    }
    expect(render()).toContain(en.empty)
  })
  it("keeps mobile folder navigation unavailable until the current file operation and reload finish", () => {
    const trigger = (props: Partial<FileBrowserProps>) =>
      render(props).match(/<button[^>]*aria-label="Open folders"[^>]*>/)?.[0]
    expect(trigger({ status: "loading" })).toContain('disabled=""')
    expect(trigger({ busy: true })).toContain('disabled=""')
    expect(trigger({})).not.toContain('disabled=""')
  })
  it("offers writes in an empty personal folder without manual root creation", () => {
    expect(render()).not.toContain(en.createPersonalRoot)
    const html = render({
      personal: true,
      onCreateDirectory: async () => true,
      onUploadEntries: async () => true,
    })
    expect(html).not.toContain(en.createPersonalRoot)
    expect(html).toContain(en.createDirectory)
    expect(html).toContain(en.upload)
  })
  it("labels system shared files independently of personal file root creation", () => {
    expect(render({ onEnsureSystemRoot: async () => true })).toContain(
      en.createSystemRoot
    )
    expect(render({ onEnsureSystemRoot: async () => true })).not.toContain(
      en.createPersonalRoot
    )
    const html = render({
      roots: [
        { id: "8", name: "System shared files", scope: "system", ownerID: "8" },
      ],
    })
    expect(html).toContain(en.rootNameSystem)
    expect(html).not.toContain(en.rootNameAccount)
  })
  it("fills its parent without introducing a nested viewport height", () => {
    const html = render()
    expect(html).toContain("h-full min-h-0 min-w-0")
    expect(html).not.toContain("calc(100svh")
  })
  it("never displays supplied stale files or roots after access fails", () => {
    for (const status of [
      "forbidden",
      "error",
      "loading",
      "selectEntity",
      "unavailable",
    ] as const) {
      const html = render({
        status,
        roots: [
          {
            id: "secret-root",
            name: "Hidden root",
            scope: "tenant",
            ownerID: "90",
            tenantID: "90",
          },
        ],
        path: [{ id: "secret-space", name: "Hidden path" }],
        nodes: [
          { id: "secret", name: "hidden.txt", kind: "file", revision: 1 },
        ],
      })
      expect(html).not.toContain("hidden.txt")
      expect(html).not.toContain('data-file-id="secret"')
      expect(html).not.toContain("Hidden root")
      expect(html).not.toContain("Hidden path")
    }
  })
  it("groups roots by organization owner scope without exposing raw owner IDs", () => {
    const html = render({
      roots: [
        {
          id: "10",
          name: "Department files",
          scope: "department",
          ownerID: "50",
          tenantID: "90",
          ownerName: "Finance",
        },
        {
          id: "12",
          name: "Department files",
          scope: "department",
          ownerID: "51",
          tenantID: "90",
          ownerName: "Operations",
        },
        {
          id: "11",
          name: "Entity files",
          scope: "entity",
          ownerID: "80",
          tenantID: "90",
          ownerName: "UAT File Company",
        },
      ],
    })
    expect(html).toContain("UAT File Company")
    expect(html).toContain(en.rootScopeDepartment)
    expect(html).toContain("Finance")
    expect(html).toContain("Operations")
    expect(html).not.toContain(">80<")
    expect(html).not.toContain(">50<")
    expect(html.indexOf(en.rootScopeEntity)).toBeLessThan(
      html.indexOf(en.rootScopeDepartment)
    )
  })
  it("rejects unbounded or duplicate-ID pages instead of quietly truncating", () => {
    const nodes = Array.from({ length: 101 }, (_, id) => ({
      id: String(id),
      name: `secret${id}`,
      kind: "file" as const,
      revision: 1,
    }))
    expect(render({ nodes })).toContain(en.invalidPage)
    expect(render({ nodes })).not.toContain("secret0")
    expect(render({ nodes: [nodes[0]!, nodes[0]!] })).toContain(en.invalidPage)
  })
  it("escapes file names and keeps selection controls opt-in", () => {
    const html = render({
      roots: [
        {
          id: "9",
          name: "Account files",
          scope: "account",
          ownerID: "7",
        },
      ],
      path: [{ id: "9", name: "Account files" }],
      nodes: [
        {
          id: "1",
          name: "<script>bad</script>",
          kind: "file",
          size: 0,
          revision: 1,
        },
      ],
      onUploadEntries: async () => true,
    })
    expect(html).not.toContain("<script>bad</script>")
    expect(html).toContain("&lt;script&gt;")
    expect(html).toContain("0 B")
    expect(html).toContain('aria-label="Selection mode"')
    expect(html).not.toContain('type="checkbox"')
    expect(html).toContain('aria-label="Details view"')
    expect(html).not.toContain("<h1")
    expect(html).toContain(">My files<")
    expect(html).not.toContain(">Account files<")
    expect(html).toContain(">Recycle bin<")
  })
  it("shows paste in the directory toolbar when a scoped clipboard is available", () => {
    const html = render({
      path: [{ id: "9", name: "Account files" }],
      clipboard: { action: "copy", count: 2 },
      onPaste: async () => true,
    })
    expect(html).toContain('aria-label="Paste 2 items"')
  })
  it("uses category-specific file icons for known formats", () => {
    const html = render({
      nodes: [
        {
          id: "1",
          name: "contract.pdf",
          kind: "file",
          detectedType: "application/pdf",
          revision: 1,
        },
        {
          id: "2",
          name: "budget.xlsx",
          kind: "file",
          extension: "xlsx",
          revision: 1,
        },
        {
          id: "3",
          name: "main.ts",
          kind: "file",
          extension: "ts",
          revision: 1,
        },
      ],
      onPreview: async () => new Blob(),
      onOpenNewTab: () => undefined,
      onMoveTo: async () => true,
    })
    expect(html).toContain('data-file-category="pdf"')
    expect(html).toContain('data-file-category="spreadsheet"')
    expect(html).toContain('data-file-category="code"')
    expect(html.match(/size-9/g)?.length).toBeGreaterThanOrEqual(3)
    expect(html.match(/data-file-open-icon/g)?.length).toBe(3)
    expect(html).toContain('aria-label="Open contract.pdf"')
    expect(html.match(/draggable="true"/g)?.length).toBe(3)
  })
  it("keeps thumbnails and fallback icons at the same visual size", () => {
    const node = {
      id: "image-1",
      name: "photo.png",
      kind: "file" as const,
      detectedType: "image/png",
      revision: 1,
    }
    const compact = renderToStaticMarkup(<FileVisual node={node} />)
    const large = renderToStaticMarkup(<FileVisual node={node} size="large" />)

    expect(compact).toContain("size-9")
    expect(compact).toContain("size-full")
    expect(large).toContain("size-16")
    expect(large).toContain("size-full")
  })
  it("keeps recycle-bin navigation and empty state separate from file roots", () => {
    const html = render({
      recycleBin: true,
      nodes: [
        {
          id: "trashed-folder",
          name: "Deleted reports",
          kind: "directory",
          revision: 2,
        },
      ],
      onOpenRecycleBin: () => undefined,
    })
    expect(html).not.toContain(en.noRoots)
    expect(html).not.toContain(en.folders)
    const navigation = html.match(
      /<nav aria-label="File roots"[\s\S]*?<\/nav>/
    )?.[0]
    expect(navigation).toBeDefined()
    expect(navigation).not.toContain("Deleted reports")
    expect(html).toContain("Deleted reports")

    const empty = render({
      recycleBin: true,
      onOpenRecycleBin: () => undefined,
    })
    expect(empty).toContain(en.emptyRecycleBin)
    expect(empty).not.toContain(en.noRoots)
    expect(empty).not.toContain(en.empty)
  })
  it("shows a localized restore location without exposing an unavailable path", () => {
    const localized = render({
      recycleBin: true,
      roots: [
        {
          id: "2",
          name: "Tenant files",
          scope: "tenant",
          ownerID: "90",
          tenantID: "90",
        },
      ],
      nodes: [
        {
          id: "4",
          name: "Deleted.txt",
          kind: "file",
          revision: 2,
          restorePath: [
            { id: "2", name: "Backend root name" },
            { id: "3", name: "Reports" },
          ],
        },
      ],
    })
    expect(localized).toContain(`${en.restoreLocation}: Tenant files / Reports`)
    expect(localized).not.toContain("Backend root name")

    const unavailable = render({
      recycleBin: true,
      nodes: [{ id: "5", name: "Hidden.txt", kind: "file", revision: 2 }],
    })
    expect(unavailable).toContain(
      `${en.restoreLocation}: ${en.restoreLocationUnavailable}`
    )
  })
  it("provides equivalent nonempty file messages in every language", () => {
    for (const messages of [zh, ms]) {
      expect(Object.keys(messages).sort()).toEqual(Object.keys(en).sort())
      for (const key of Object.keys(en) as (keyof typeof en)[]) {
        expect(messages[key].trim()).not.toBe("")
        expect(messages[key].match(/\{\w+\}/g)?.sort() ?? []).toEqual(
          en[key].match(/\{\w+\}/g)?.sort() ?? []
        )
      }
    }
  })
})
