import {expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ProjectClientField, ProjectContactsField, ProjectMembersField, ProjectRepositoriesField} from "./create-fields"

for (const locale of ["en-US", "zh-CN", "ms-MY"]) it(`renders exact nested values and localized row controls: ${locale}`, async () => {
  const messages = await loadMessages(locale)
  const props = {name: "test", config: {}, required: false, disabled: false, onChange: () => {}}
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}>
    <ProjectClientField {...props} value={{entity_id: "9007199254740993"}}/>
    <ProjectContactsField {...props} value={[{intake_contact_id: "9007199254740995", role: "CC", receives_deliveries: false, can_accept: false}]}/>
    <ProjectRepositoriesField {...props} value={[{name: "Source", remote_url: "git@example.invalid:team/repo.git", default_branch: "release", protected_branches: ["release", "production"], is_primary: false}]}/>
    <ProjectMembersField {...props} value={[{principal_id: "9007199254740997", role_key: "engineer"}]}/>
  </IntlAppProvider>)
  const labels = messages.iam as Record<string, string>
  for (const key of ["projectClientChoice", "projectContactAdd", "projectRepositoryAdd", "projectMemberAdd", "projectMemberHint"]) {
    expect(html).toContain(labels[key]!)
    expect(labels[key]).not.toContain("?")
  }
  for (const value of ["9007199254740993", "9007199254740995", "9007199254740997", "release, production", "engineer"]) expect(html).toContain(value)
  expect(html).not.toContain('checked=""')
})

it("disables add controls at the published contact, repository and member limits", async () => {
  const messages = await loadMessages("en-US")
  const props = {name: "test", config: {}, required: false, disabled: false, onChange: () => {}}
  const render = (element: React.ReactNode) => renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}>{element}</IntlAppProvider>)
  for (const [Component, count, value] of [
    [ProjectContactsField, 20, {intake_contact_id: "1", role: "CC", receives_deliveries: false, can_accept: false}],
    [ProjectRepositoriesField, 10, {name: "Source", remote_url: "https://example.invalid/repo.git"}],
    [ProjectMembersField, 50, {principal_id: "1", role_key: "engineer"}],
  ] as const) {
    const atLimit = render(<Component {...props} value={Array.from({length: count}, () => value)}/>)
    expect(atLimit).toMatch(/<button[^>]*disabled=""[^>]*>Add /)
    const belowLimit = render(<Component {...props} value={Array.from({length: count-1}, () => value)}/>)
    expect(belowLimit).not.toMatch(/<button[^>]*disabled=""[^>]*>Add /)
  }
})
