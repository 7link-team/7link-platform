import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlProvider } from "use-intl"
import { AutoForm } from "./auto-form"
import messages from "../../i18n/messages/en-US.json"

describe("AutoForm native input compatibility", () => {
  it("preserves password masking, email semantics, IDs and explicit reveal controls", () => {
    const html = renderToStaticMarkup(
      <IntlProvider locale="en-US" timeZone="UTC" messages={messages}>
        <AutoForm
          fields={{
            secret: {
              name: "Password",
              type: "password",
              config: {
                id: "secret",
                autoComplete: "new-password",
                minLength: 12,
                maxLength: 1024,
              },
            },
            email: { name: "Email", type: "email" },
            revealed: {
              name: "Revealed",
              type: "password",
              config: { type: "text" },
            },
          }}
        />
      </IntlProvider>
    )
    expect(html).toContain('id="secret"')
    expect(html).toContain('type="password"')
    expect(html).toContain('type="email"')
    expect(html).toContain('type="text"')
    expect(html).toContain('autoComplete="new-password"')
    expect(html).toContain('minLength="12"')
    expect(html).toContain('maxLength="1024"')
  })
})
