import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { VerifierRegistry } from "./verifier-registry"
import { verifierRegistryMessages } from "./verifier-registry-data"

for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`renders registry policy filters and permission-scoped actions in ${locale}`, async () => {
  const messages = await loadMessages(locale), l = verifierRegistryMessages[locale]
  const render = (permissions: string[]) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter initialEntries={["/?tab=verifiers&verifier_state=ARCHIVED&verifier_project=9007199254740993"]}><Routes><Route element={<Outlet context={{permissions: new Set(permissions), permissionsResolved: true}} />}><Route index element={<VerifierRegistry tenantID="7" />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
  const owner = render(["policy_read", "verifier_manage"])
  expect(owner).toContain(l.title); expect(owner).toContain(l.hint); expect(owner).toContain(l.add); expect(owner).toContain(l.projects); expect(owner).toContain("9007199254740993")
  expect(owner).toContain(l.ARCHIVED); expect(owner).not.toContain("undefined")
  const reader = render(["policy_read"]); expect(reader).not.toContain(l.add); expect(reader).toContain(l.title)
  const denied = render([]); expect(denied).toContain(l.readonly); expect(denied).not.toContain("<form"); expect(denied).not.toContain(l.add)
})
