import { ApiError, iamApi, type FilePage } from "./iam-api"
import {
  fileBrowserChildren,
  fileBrowserEntries,
  fileBrowserRoots,
  fileID,
  mergeFileCollectionPages,
  type FileBrowserLocation,
  type FileBrowserNode,
  type FileBrowserRoot,
} from "./file-browser"

// What decides which collections a file view reads from. The directory and the
// page within it are not part of it: moving between directories reads one
// directory, not every collection again.
export type FileCollectionScope = {
  account: boolean
  system: boolean
  tenantID: string
  entityID: string
  canViewDepartments: boolean
  recycleBin: boolean
  reload: number
}

export type FileBrowserView = {
  roots: FileBrowserRoot[]
  path: FileBrowserLocation[]
  nodes: FileBrowserNode[]
  nextAfter: string
  // The directory listed, or null for the recycle bin and an empty view.
  directoryID: string | null
}

type FileCollectionAPI = Pick<
  typeof iamApi,
  "files" | "departmentFiles" | "departments" | "memberEntities" | "fileItems"
>

type FileCollections = {
  roots: FileBrowserRoot[]
  trashed: (after: string) => Promise<FilePage>
}

type CollectionPage = (
  after: string,
  state: "active" | "trashed"
) => Promise<FilePage>

function fileEntityID(scope: FileCollectionScope) {
  return scope.account ? null : scope.entityID || undefined
}

async function loadFileCollections(
  api: FileCollectionAPI,
  scope: FileCollectionScope
): Promise<FileCollections> {
  const entityID = fileEntityID(scope)
  if (scope.account || scope.system)
    return {
      roots: fileBrowserRoots(await api.files(entityID, "", scope.system)),
      trashed: (after) => api.files(entityID, after, scope.system, "trashed"),
    }
  const [entities, departments] = await Promise.all([
    scope.entityID
      ? Promise.resolve([])
      : api.memberEntities("file_view", scope.tenantID),
    scope.canViewDepartments ? api.departments() : Promise.resolve([]),
  ])
  const visibleDepartments = departments.filter(
    (department) => !scope.entityID || department.entity_id === scope.entityID
  )
  const entityIDs = scope.entityID
    ? [scope.entityID]
    : entities.map((entity) => entity.id)
  const collections: CollectionPage[] = [
    ...(!scope.entityID
      ? [
          (after: string, state: "active" | "trashed") =>
            api.files(undefined, after, false, state),
        ]
      : []),
    ...entityIDs.map(
      (id) => (after: string, state: "active" | "trashed") =>
        api.files(id, after, false, state)
    ),
    ...visibleDepartments.map(
      (department) => (after: string, state: "active" | "trashed") =>
        api
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
          (!fileID.test(page.next_after) ||
            BigInt(page.next_after) <= BigInt(after || "0"))
        )
          throw new Error("invalid_response")
        after = page.next_after ?? ""
      } while (after)
      return pages
    })
  )
  const names = Object.fromEntries(
    [...entities, ...visibleDepartments].map((owner) => [owner.id, owner.name])
  )
  return {
    roots: rootPages.flat().flatMap((page) => fileBrowserRoots(page, names)),
    trashed: async (after) =>
      mergeFileCollectionPages(
        await Promise.all(collections.map((load) => load(after, "trashed")))
      ),
  }
}

// fileBrowserViewLoader reads one file view: the collections of a scope, then
// the directory or recycle-bin page asked for. The collections are read once per
// scope and kept, so moving between directories and pages costs one listing.
export function fileBrowserViewLoader(api: FileCollectionAPI = iamApi) {
  let cached: { key: string; load: Promise<FileCollections> } | null = null
  return async (
    scope: FileCollectionScope,
    directoryID: string | null,
    after: string
  ): Promise<FileBrowserView> => {
    const key = JSON.stringify(scope)
    let entry = cached
    if (!entry || entry.key !== key) {
      const load = loadFileCollections(api, scope)
      entry = cached = { key, load }
      // A failed read is not kept: the next view asks again.
      load.catch(() => {
        if (cached?.load === load) cached = null
      })
    }
    const { roots, trashed } = await entry.load
    if (scope.recycleBin)
      return {
        roots,
        path: [],
        ...fileBrowserEntries(await trashed(after)),
        directoryID: null,
      }
    const selected = directoryID ?? roots[0]?.id ?? null
    if (!selected)
      return { roots, path: [], nodes: [], nextAfter: "", directoryID: null }
    return {
      roots,
      ...fileBrowserChildren(
        await api.fileItems(fileEntityID(scope), selected, after)
      ),
      directoryID: selected,
    }
  }
}
