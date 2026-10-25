import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { TemplateBinding } from "./binding"
import { bindingLoadError } from "./api"
import { ApiError } from "../../lib/iam-api"
import { loadMessages } from "../../lib/i18n"

describe("Template binding access and disclosure", () => {
  it("treats only the explicit missing binding as empty and preserves real failures", () => {
    expect(bindingLoadError(new ApiError(404, "missing", "Template not found", undefined, 185404))).toBe("")
    for (const error of [new ApiError(403, "forbidden", "Forbidden"), new ApiError(503, "unavailable", "Unavailable"), new ApiError(422, "invalid", "Invalid configuration"), new ApiError(404, "missing", "Other resource missing", undefined, 404)]) expect(bindingLoadError(error)).toBe(error.message)
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) it(`discloses pinned history and initial binding requirement in ${locale}`, async () => {
    const messages = await loadMessages(locale), strings = messages.iam as Record<string, string>
    const render = (permissions: string[]) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(permissions), permissionsResolved: true }} />}><Route index element={<TemplateBinding tenant="1" project="99" />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
    const html = render(["template_read", "work_item_read", "project_manage"])
    expect(html).toContain(strings.bindingTitle!)
    expect(html).toContain(strings.bindingHistoryHint!)
    expect(html).toContain(strings.bindingLoadHint!)
    expect(html).toContain('/admin/1/templates')
    expect(render(["template_read"])).toBe("")
    expect(strings.bindingOverrideHint!.length).toBeGreaterThan(20)
    expect(strings.bindingMajorAck!.length).toBeGreaterThan(10)
  })
})
