// Shared route/menu declarations keep every project entry in the same left
// menu set. Permissions offer existing facts; unavailable workflows stay clear.
export const projectSurfaces = [
  { path: "intake", label: "intakeWorkbenchTitle", permission: "scope_change", alternatePermission: "intake_convert", triagePermission: "intake_triage", submissionPermission: "intake_submit" },
  { path: "heavy", label: "heavyTitle", permission: "work_item_read" },
  { path: "todo", label: "clientPendingTitle", permission: "channel_read" },
  { path: "baseline", label: "projectSurfaceBaseline", permission: "channel_read" },
  { path: "changes", label: "projectSurfaceChanges", permission: "channel_read" },
  { path: "feedback", label: "projectSurfaceFeedback", permission: "channel_read" },
  { path: "review", label: "projectSurfaceReview", permission: "work_item_read" },
  { path: "defects", label: "projectSurfaceDefects", permission: "work_item_read" },
  { path: "test-cases", label: "projectSurfaceTests", permission: "project_read" },
  { path: "pipelines", label: "projectSurfacePipelines", permission: "project_read" },
  { path: "previews", label: "projectSurfacePreviews", permission: "channel_read" },
  { path: "deliveries", label: "projectSurfaceDeliveries", permission: "channel_read" },
  { path: "dispatch", label: "projectSurfaceDispatch", permission: "work_item_assign" },
  { path: "exceptions", label: "projectSurfaceExceptions", permission: "project_read" },
  { path: "project-settings", label: "projectSurfaceSettings", permission: "project_manage" },
  { path: "video", label: "studioVIDEO", permission: "work_item_read" },
  { path: "novel", label: "studioNOVEL", permission: "work_item_read" },
  { path: "team-tasks", label: "studioTEAM_TASKS", permission: "work_item_read" },
] as const

export type ProjectSurface = (typeof projectSurfaces)[number]["path"]

export function projectSurface(path: string) {
  return projectSurfaces.find((entry) => entry.path === path)
}
