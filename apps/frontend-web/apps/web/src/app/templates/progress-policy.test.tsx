import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { ProgressPolicyEditor } from "./progress-policy"

test("published progress policy cannot be edited and has no invented threshold", async () => {
 const messages = await loadMessages("en-US")
 const render = (value: unknown, disabled: boolean) => renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><ProgressPolicyEditor value={value} disabled={disabled} onChange={() => {}}/></IntlAppProvider>)
 const absent = render(undefined, false); expect(absent).not.toContain('type="number"')
 const pinned = render({no_progress_limit: 7, dependency_miss_limit: 4, observable_paths: ["out.txt"], required_dependency_paths: ["input.txt"]}, true)
 expect(pinned).toContain('value="7"'); expect(pinned).toContain('value="4"'); expect(pinned).toContain("out.txt"); expect(pinned).toContain("input.txt"); expect(pinned).toContain("disabled"); expect(pinned).toContain("publication")
})
