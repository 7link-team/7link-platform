import { describe, expect, it } from "bun:test"
import {
  classifyFilePresentation,
  fileBrowserChildren,
  fileBrowserEntries,
  fileBrowserRoots,
  fileSizeLabel,
  materializeDroppedEntries,
  parentFileBrowserDirectoryID,
  parseDroppedEntries,
  selectFileRange,
  visibleFileNodes,
  type DroppedDataTransferItem,
  type DroppedFileSystemEntry,
  type FileBrowserNode,
} from "./file-browser"

const nodes: FileBrowserNode[] = [
  {
    id: "2",
    name: "file10.txt",
    kind: "file",
    size: 2048,
    updatedAt: "2026-09-18",
    revision: 1,
  },
  {
    id: "1",
    name: "file2.txt",
    kind: "file",
    size: 0,
    updatedAt: "2026-09-17",
    revision: 1,
  },
  { id: "3", name: "café", kind: "directory", revision: 1 },
]

describe("file browser navigation", () => {
  it("derives a stable parent from the authorized directory path", () => {
    expect(
      parentFileBrowserDirectoryID([
        { id: "root", name: "My files" },
        { id: "parent", name: "Projects" },
        { id: "current", name: "Current" },
      ])
    ).toBe("parent")
    expect(
      parentFileBrowserDirectoryID([{ id: "root", name: "My files" }])
    ).toBeUndefined()
    expect(parentFileBrowserDirectoryID([])).toBeUndefined()
  })
})

describe("file browser presentation", () => {
  it("classifies known files by detected MIME before extension with safe preview tiers", () => {
    expect(
      classifyFilePresentation({
        name: "renamed.bin",
        detectedType: "application/pdf; charset=binary",
      })
    ).toMatchObject({ category: "pdf", preview: "pdf", editable: false })
    expect(
      classifyFilePresentation({
        name: "budget.xlsx",
        detectedType:
          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      })
    ).toMatchObject({
      category: "spreadsheet",
      preview: "office",
      badge: "XLSX",
    })
    expect(classifyFilePresentation({ name: "main.ts" })).toMatchObject({
      category: "code",
      preview: "text",
      editable: true,
    })
    expect(
      classifyFilePresentation({
        name: "README",
        detectedType: "text/plain",
      })
    ).toMatchObject({ category: "text", preview: "text", editable: true })
    expect(classifyFilePresentation({ name: "backup.zip" })).toMatchObject({
      category: "archive",
      preview: "unsupported",
    })
    expect(
      classifyFilePresentation({
        name: "report.docx",
        detectedType: "application/zip",
      })
    ).toMatchObject({ category: "document", preview: "office" })
    expect(
      classifyFilePresentation({
        name: "slides.pptx",
        detectedType:
          "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      })
    ).toMatchObject({ category: "presentation", preview: "office" })
    expect(
      classifyFilePresentation({
        name: "backup.zip",
        detectedType: "application/zip",
      })
    ).toMatchObject({ category: "archive", preview: "unsupported" })
    expect(classifyFilePresentation({ name: "opaque" })).toMatchObject({
      category: "unknown",
      preview: "unsupported",
      editable: false,
    })
  })

  it("sorts naturally with folders first without mutating authorized snapshots", () => {
    expect(
      visibleFileNodes(nodes, "", "all", "name", false, "en-US").map(
        (node) => node.id
      )
    ).toEqual(["3", "1", "2"])
    expect(
      visibleFileNodes(nodes, "", "all", "name", true, "en-US").map(
        (node) => node.id
      )
    ).toEqual(["3", "2", "1"])
    expect(nodes.map((node) => node.id)).toEqual(["2", "1", "3"])
  })
  it("filters normalized names and types only within the supplied page", () => {
    expect(
      visibleFileNodes(
        nodes,
        " CAFE\u0301 ",
        "directory",
        "name",
        false,
        "en-US"
      ).map((node) => node.id)
    ).toEqual(["3"])
    expect(
      visibleFileNodes(nodes, "file", "directory", "name", false, "en-US")
    ).toEqual([])
  })
  it("sorts sizes and timestamps with stable ID ties", () => {
    expect(
      visibleFileNodes(nodes, "", "file", "size", false, "en-US").map(
        (node) => node.id
      )
    ).toEqual(["1", "2"])
    expect(
      visibleFileNodes(nodes, "", "file", "updated", true, "en-US").map(
        (node) => node.id
      )
    ).toEqual(["2", "1"])
    expect(
      visibleFileNodes(
        [
          { id: "z", name: "x", kind: "file", revision: 1 },
          { id: "a", name: "x", kind: "file", revision: 1 },
        ],
        "",
        "all",
        "updated",
        false,
        "en-US"
      ).map((node) => node.id)
    ).toEqual(["a", "z"])
  })
  it("toggles by ID and ranges in current displayed order, including backwards", () => {
    const initial = new Set(["2"])
    expect([...selectFileRange(initial, nodes, "2", null, false)]).toEqual([])
    expect([...initial]).toEqual(["2"])
    expect([...selectFileRange(new Set(), nodes, "3", "2", true)]).toEqual([
      "2",
      "1",
      "3",
    ])
    expect([...selectFileRange(new Set(), nodes, "2", "3", true)]).toEqual([
      "2",
      "1",
      "3",
    ])
  })
  it("cannot range through filtered-out anchors or select unknown IDs", () => {
    expect([
      ...selectFileRange(new Set(), nodes.slice(0, 2), "1", "3", true),
    ]).toEqual(["1"])
    expect([
      ...selectFileRange(new Set(["1"]), nodes, "missing", "1", true),
    ]).toEqual(["1"])
  })
  it("formats zero, large and unavailable sizes without guessing", () => {
    expect(fileSizeLabel(0, "en-US")).toBe("0 B")
    expect(fileSizeLabel(2048, "en-US")).toBe("2 KiB")
    expect(fileSizeLabel(2 ** 31, "en-US")).toBe("2 GiB")
    for (const invalid of [undefined, -1, NaN, Infinity])
      expect(fileSizeLabel(invalid, "en-US")).toBe("\u2014")
  })
  it("maps only bounded, valid authorized API snapshots", () => {
    expect(
      fileBrowserRoots({
        items: [
          {
            id: "2",
            name: "Personal files",
            kind: "directory",
            scope: "personal",
            tenant_id: "90",
            principal_id: "70",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 1,
          },
        ],
      })
    ).toEqual([
      {
        id: "2",
        name: "Personal files",
        scope: "personal",
        ownerID: "70",
        tenantID: "90",
      },
    ])
    expect(
      fileBrowserChildren({
        path: [{ id: "2", name: "Tenant" }],
        items: [
          {
            id: "3",
            name: "Report.pdf",
            kind: "file",
            size: 42,
            updated_at: "2026-09-18T12:00:00Z",
            revision: 4,
            version: 2,
          },
        ],
        next_after: "3",
      })
    ).toEqual({
      path: [{ id: "2", name: "Tenant" }],
      nodes: [
        {
          id: "3",
          name: "Report.pdf",
          kind: "file",
          size: 42,
          updatedAt: "2026-09-18T12:00:00Z",
          detectedType: undefined,
          extension: undefined,
          version: "2",
          revision: 4,
        },
      ],
      nextAfter: "3",
    })
    expect(
      fileBrowserEntries({
        items: [
          {
            id: "4",
            name: "Deleted.txt",
            kind: "file",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 5,
            restore_path: [
              { id: "2", name: "Tenant files" },
              { id: "3", name: "Reports" },
            ],
          },
        ],
      })
    ).toEqual({
      nodes: [
        {
          id: "4",
          name: "Deleted.txt",
          kind: "file",
          size: undefined,
          updatedAt: "2026-09-18T12:00:00Z",
          detectedType: undefined,
          extension: undefined,
          version: undefined,
          revision: 5,
          restorePath: [
            { id: "2", name: "Tenant files" },
            { id: "3", name: "Reports" },
          ],
        },
      ],
      nextAfter: "",
    })
  })
  it("maps department owner IDs to readable names", () => {
    expect(
      fileBrowserRoots(
        {
          items: [
            {
              id: "2",
              name: "department-50",
              kind: "directory",
              scope: "department",
              tenant_id: "90",
              department_id: "50",
              updated_at: "2026-09-18T12:00:00Z",
              revision: 1,
            },
          ],
        },
        { "50": "Finance" }
      )[0]?.ownerName
    ).toBe("Finance")
  })
  it("rejects malformed or unbounded API snapshots", () => {
    expect(() =>
      fileBrowserEntries({
        items: [
          {
            id: "4",
            name: "Deleted.txt",
            kind: "file",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 5,
            restore_path: [{ id: "not-an-id", name: "Secret" }],
          },
        ],
      })
    ).toThrow("invalid_response")
    expect(() =>
      fileBrowserRoots({
        items: [
          {
            id: "0",
            name: "Hidden",
            kind: "directory",
            scope: "personal",
            tenant_id: "90",
            principal_id: "70",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 1,
          },
        ],
      })
    ).toThrow("invalid_response")
    expect(() =>
      fileBrowserRoots({
        items: Array.from({ length: 101 }, (_, index) => ({
          id: String(index + 1),
          name: `Root ${index}`,
          kind: "directory",
          scope: "personal",
          tenant_id: "90",
          principal_id: "70",
          updated_at: "2026-09-18T12:00:00Z",
          revision: 1,
        })),
      })
    ).toThrow("invalid_response")
    expect(() =>
      fileBrowserRoots({
        items: [
          {
            id: "2",
            name: "Department files",
            kind: "directory",
            scope: "department",
            tenant_id: "90",
            entity_id: "80",
            department_id: "50",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 1,
          },
        ],
      })
    ).toThrow("invalid_response")
    expect(() =>
      fileBrowserChildren({
        path: [{ id: "2", name: "Tenant" }],
        items: [
          {
            id: "3",
            name: "one",
            kind: "directory",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 1,
            version: 1,
          },
          {
            id: "3",
            name: "two",
            kind: "directory",
            updated_at: "2026-09-18T12:00:00Z",
            revision: 1,
            version: 1,
          },
        ],
        next_after: "invalid",
      })
    ).toThrow("invalid_response")
  })

  it("reads multiple dropped files with the plain file-list fallback", async () => {
    const files = [new File(["one"], "one.txt"), new File(["two"], "two.txt")]
    const entries = await parseDroppedEntries([], files)
    expect(entries.map(({ kind, name }) => [kind, name])).toEqual([
      ["file", "one.txt"],
      ["file", "two.txt"],
    ])
  })

  it("reads nested and empty dropped directories across every reader batch", async () => {
    const fileEntry = (name: string): DroppedFileSystemEntry => ({
      name,
      isFile: true,
      isDirectory: false,
      file: (success) => success(new File([name], name)),
    })
    const directoryEntry = (
      name: string,
      batches: DroppedFileSystemEntry[][]
    ): DroppedFileSystemEntry => ({
      name,
      isFile: false,
      isDirectory: true,
      createReader: () => {
        let index = 0
        return {
          readEntries: (success) => success(batches[index++] ?? []),
        }
      },
    })
    const root = directoryEntry("reports", [
      [fileEntry("one.txt")],
      [directoryEntry("empty", [[]]), fileEntry("two.txt")],
      [],
    ])
    const item: DroppedDataTransferItem = {
      kind: "file",
      getAsFile: () => null,
      webkitGetAsEntry: () => root,
    }

    expect(await parseDroppedEntries([item], [])).toEqual([
      {
        kind: "directory",
        name: "reports",
        children: [
          {
            kind: "file",
            name: "one.txt",
            file: expect.any(File),
          },
          { kind: "directory", name: "empty", children: [] },
          {
            kind: "file",
            name: "two.txt",
            file: expect.any(File),
          },
        ],
      },
    ])
  })

  it("bounds recursive directory depth and total dropped entries", async () => {
    const directory = (child?: DroppedFileSystemEntry) =>
      ({
        name: "folder",
        isFile: false,
        isDirectory: true,
        createReader: () => {
          let sent = false
          return {
            readEntries: (
              success: (entries: DroppedFileSystemEntry[]) => void
            ) => {
              const entries = sent || !child ? [] : [child]
              sent = true
              success(entries)
            },
          }
        },
      }) satisfies DroppedFileSystemEntry
    const item = (entry: DroppedFileSystemEntry): DroppedDataTransferItem => ({
      kind: "file",
      getAsFile: () => null,
      webkitGetAsEntry: () => entry,
    })

    await expect(
      parseDroppedEntries([item(directory(directory()))], [], { maxDepth: 1 })
    ).rejects.toThrow("drop_too_deep")
    await expect(
      parseDroppedEntries([], [new File([], "one"), new File([], "two")], {
        maxEntries: 1,
      })
    ).rejects.toThrow("drop_too_many_entries")
  })

  it("creates dropped directories before recursively uploading their contents", async () => {
    const operations: string[] = []
    await materializeDroppedEntries(
      "root",
      [
        {
          kind: "directory",
          name: "reports",
          children: [
            { kind: "directory", name: "empty", children: [] },
            {
              kind: "file",
              name: "report.txt",
              file: new File(["report"], "report.txt"),
            },
          ],
        },
        {
          kind: "file",
          name: "readme.txt",
          file: new File(["readme"], "readme.txt"),
        },
      ],
      async (parentID, name) => {
        operations.push(`mkdir:${parentID}:${name}`)
        return `${parentID}/${name}`
      },
      async (parentID, file) => {
        operations.push(`upload:${parentID}:${file.name}`)
      }
    )
    expect(operations).toEqual([
      "mkdir:root:reports",
      "mkdir:root/reports:empty",
      "upload:root/reports:report.txt",
      "upload:root:readme.txt",
    ])
  })
})
