import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {NativeSearch} from "./native-search"
import {EmbeddingSettings} from "./embedding-settings"

describe("knowledge search and embedding controls", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`labels controls and capability limits in ${locale}`, async () => {
    const messages = await loadMessages(locale)
    const words = messages.iam as Record<string, string>
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><NativeSearch tenant="19" project="9007199254740995"/><EmbeddingSettings tenant="19"/></MemoryRouter></IntlAppProvider>)
    expect(html).toContain(words.knowledgeNativeSearch!)
	expect(html).toContain(words.archiveSearchInclude!)
    expect(html).toContain(words.knowledgeNativeMatch!)
    expect(html).toContain(words.knowledgeEmbeddingTitle!)
    expect(html).toContain(words.knowledgeEmbeddingHint!)
    expect(html).toContain(words.knowledgeNativeAllScopes!)
    expect(html).toContain(words.knowledgeNativeUpdatedAfter!)
    expect(html).toContain(words.knowledgeNativeUpdatedBefore!)
    expect(html).toContain('type="datetime-local"')
    expect(html).toContain('value="ALL"')
    expect(html).toContain('type="password"')
    expect(html).not.toContain("knowledgeEmbeddingTitle")
    expect(html).not.toContain("<mark>")
  })
})
