import {expect, test} from "bun:test"
import {workspaceTab} from "../runtime/facts"

test("workspace deep links restore a supported tab and reject unrelated URL state", () => {
  const url = new URL("https://app.example.test/executions/9007199254740993/workspace?tab=remote&before_id=99")
  expect(workspaceTab(url.searchParams.get("tab"))).toBe("remote")
  url.searchParams.set("tab", "evidence")
  expect(workspaceTab(url.searchParams.get("tab"))).toBe("evidence")
  expect(url.searchParams.get("before_id")).toBe("99")
  expect(workspaceTab("terminal_session:other-execution")).toBe("logs")
  expect(workspaceTab(null)).toBe("logs")
})
