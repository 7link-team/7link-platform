import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"

// The page is effect-driven and static markup runs no effects, so this pins the
// mechanism: every collection read goes through one fileBrowserViewLoader kept
// for the page's lifetime, whose tests show a directory change reads only that
// directory. Reading a collection here directly is how every click on a folder
// came to read every collection again.
describe("files page collection reads", () => {
  const source = readFileSync(new URL("./page.tsx", import.meta.url), "utf8")
  it("reads collections only through one kept view loader", () => {
    expect(source).toContain(
      "const [loadView] = useState(() => fileBrowserViewLoader())"
    )
    for (const read of [
      "iamApi.files(",
      "iamApi.departmentFiles(",
      "iamApi.memberEntities(",
      "iamApi.departments(",
      "iamApi.fileItems(",
    ])
      expect(source).not.toContain(read)
  })
})
