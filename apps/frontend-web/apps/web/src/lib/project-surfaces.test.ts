import { describe, expect, it } from "bun:test"
import { projectSurfaces } from "./project-surfaces"
import { entityNavigation, tenantSystemNavigation, workspaceArea, workspaceScopePath } from "./navigation"

describe("project view ownership and navigation", () => {
  it("puts every project surface in exactly one collaboration menu with its guard", () => {
    for (const entry of projectSurfaces) {
      const path = `/${entry.path}`
      expect(workspaceArea(path)).toBe("collaboration")
      expect(workspaceArea(`/admin/1/entities/7${path}`)).toBe("collaboration")
      expect(entityNavigation([entry.permission], "project").filter((row) => row.path === path)).toHaveLength(1)
      expect(entityNavigation([], "project").some((row) => row.path === path)).toBe(false)
      expect(entityNavigation([entry.permission], "department").some((row) => row.path === path)).toBe(false)
      expect(workspaceScopePath(`/admin/1/entities/7${path}`, "1", "8", [entry.permission], "project")).toBe(`/admin/1/entities/8${path}`)
      expect(workspaceScopePath(`/admin/1/entities/7${path}`, "1", "8", [], "project")).toBeNull()
      expect(workspaceScopePath(`/admin/1/entities/7${path}`, "1", "", [entry.permission])).toBeNull()
    }
  })
  it("offers my work once in the workbench for both project and tenant scope", () => {
    expect(workspaceArea("/my-work")).toBe("workbench")
    expect(entityNavigation(["work_item_read"], "project").filter((row) => row.path === "/my-work")).toHaveLength(1)
    expect(tenantSystemNavigation(new Set(), false, false, true, false, false, false, ["work_item_read"]).filter((row) => row.path === "/my-work")).toHaveLength(1)
    expect(tenantSystemNavigation(new Set(["/my-work"]), false, false, true, false, false, false, ["work_item_read"]).filter((row) => row.path === "/my-work")).toHaveLength(0)
    expect(tenantSystemNavigation(new Set(), false, false, true, false, false, false, ["project_read"]).some((row) => row.path === "/my-work")).toBe(false)
    expect(workspaceScopePath("/admin/1/my-work", "1", "8", ["work_item_read"], "project")).toBe("/admin/1/entities/8/my-work")
    expect(workspaceScopePath("/admin/1/my-work", "1", "8", [], "department")).toBeNull()
    expect(workspaceScopePath("/admin/1/my-work", "1", "8", [], "project")).toBeNull()
  })
})

it("keeps channel-only client navigation within the server projection",()=>{
 const links=entityNavigation(["channel_read"],"project").map(v=>v.path)
 expect(links).toEqual(["/conversations","/todo","/baseline","/changes","/feedback","/previews","/deliveries"])
 for(const internal of ["/work-items","/review","/defects","/test-cases","/pipelines","/project-settings","/files"])expect(links).not.toContain(internal)
})

it("offers the narrow Intake conversion surface without general scope or requirement write access",()=>{
 expect(entityNavigation(["intake_convert"],"project").filter(row=>row.path==="/intake")).toHaveLength(1)
 expect(entityNavigation(["intake_convert"],"department").some(row=>row.path==="/intake")).toBe(false)
 expect(workspaceScopePath("/admin/1/entities/7/intake","1","8",["intake_convert"],"project")).toBe("/admin/1/entities/8/intake")
 expect(entityNavigation(["intake_convert"],"project").some(row=>["/requirements","/work-items","/project-settings"].includes(row.path))).toBe(false)
})

it("offers triage-only project navigation without broader write surfaces",()=>{
 expect(entityNavigation(["intake_triage"],"project").filter(row=>row.path==="/intake")).toHaveLength(1)
 expect(workspaceScopePath("/admin/1/entities/7/intake","1","8",["intake_triage"],"project")).toBe("/admin/1/entities/8/intake")
 expect(entityNavigation(["intake_triage"],"department").some(row=>row.path==="/intake")).toBe(false)
})
