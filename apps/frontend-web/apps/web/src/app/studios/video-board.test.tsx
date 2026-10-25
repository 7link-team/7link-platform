import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import VideoBoard from "./video-board"

test("video studio restores all five tabs in three locales with honest provider and QC absence", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  for (const tab of ["shots", "references", "qc", "master", "providers"]) {
   const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(["template_read", "work_item_read", "project_manage"]), permissionsResolved: true}}/>, children: [{path: "/", element: <VideoBoard tenant="7" project="99"/>}]}], {initialEntries: [`/?video_tab=${tab}`]})
   const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
   expect(html).toContain(`aria-labelledby="video-tab-${tab}"`); expect(html.match(/role="tab"/g)).toHaveLength(5)
   expect(html).not.toContain("iam.video"); expect(html).not.toContain("videoTab_"); expect(html).not.toContain("????")
   if (tab === "providers") expect(html).toContain("UNAVAILABLE")
   expect(html).not.toContain("<video")
   router.dispose()
  }
 }
})
