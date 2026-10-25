import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { ReviewTypes } from "./review-types"
import en from "../../messages/en-US.json"
import ui from "@workspace/ui/i18n/messages/en-US.json"
it("states propagation limits and hides review-type editing without management permission", () => {
  const html = renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}>
      <ReviewTypes tenant="1" manage={false} onTypes={() => undefined} />
    </IntlAppProvider>
  )
  expect(html).toContain("Open reviews keep their snapshot")
  expect(html).toContain(
    "Approval binds the frozen acceptance suite to the requirement version"
  )
  expect(html).not.toContain("Add review type")
})
