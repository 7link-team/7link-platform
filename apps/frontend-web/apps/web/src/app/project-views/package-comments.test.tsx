import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { PackageComments, RefusalAnchors } from "./package-comments"

it("renders immutable refusal source safely in three locales without duplicate DOM identities",async()=>{
  for(const locale of ["en-US","zh-CN","ms-MY"]){
    const messages=await loadMessages(locale),labels=messages.iam as Record<string,string>
    const anchor={thread_id:"9007199254740993",anchor_kind:"TEXT_PARAGRAPH",anchor_excerpt:"<script>source quote</script>",version_ref:"file-version:9007199254740995 sha256:frozen",artifact_ref:{}}
    const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RefusalAnchors anchors={[anchor]}/><RefusalAnchors anchors={[anchor]}/></IntlAppProvider>)
    expect(html).toContain(labels.packageReferencedComments!);expect(html).toContain(anchor.thread_id);expect(html).toContain(anchor.version_ref);expect(html).toContain("&lt;script&gt;source quote&lt;/script&gt;");expect(html).not.toContain("<script>");expect(html).not.toContain('id="client-comment-')
    const team=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><PackageComments project="3" delivery="8" item={"a".repeat(64)} external={false} published/></IntlAppProvider>)
    expect(team).toContain(labels.packageClientCommentsReadOnly!);expect(team).not.toContain("<textarea");expect(team).not.toContain("<select")
    const client=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><PackageComments project="3" delivery="8" item={"a".repeat(64)} external published/></IntlAppProvider>)
    for(const kind of ["CODE_LINES","TABLE_ROW","TEXT_PARAGRAPH","HTML_REGION"])expect(client).toContain(labels[`packageCommentKind${kind}`]!)
    expect(client).toContain(labels.packageCommentBody!)
  }
})
