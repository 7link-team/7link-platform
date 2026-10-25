import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ConversationMarkdown } from "./message"
import type { SystemMessage } from "./system-message-folding"
import { ChannelSearchCard } from "./media-tools"
import { readSearchCard } from "./media-api"
export function SystemMessageContent({ message, tenant, project }: { message: SystemMessage; tenant: string; project: string }) {
  const t = useTranslations("iam")
  if (!message.kind || message.kind === "TEXT") return <ConversationMarkdown body={message.body} />
  if (message.kind === "SEARCH_CARD") {
    const card = readSearchCard(message.payload)
    return card ? <ChannelSearchCard card={card} /> : <ConversationMarkdown body={message.body} />
  }
  const proposal = message.payload?.proposal_id
  const work = message.payload?.work_item_id ?? (message.payload?.ref_kind === "work_item" ? message.payload.ref_id : undefined)
  const base = `/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}`
  return <section className={`space-y-2 rounded border p-3 ${message.attention ? "border-destructive" : ""}`}>
    <p className="font-medium">{t(`conversationKind_${message.kind}`)}{message.attention ? ` ? ${t("conversationAttention")}` : ""}</p><ConversationMarkdown body={message.body} />
    {typeof message.payload?.status === "string" && <p className="text-sm">{message.payload.status}</p>}
    {typeof proposal === "string" && /^[1-9][0-9]{0,18}$/.test(proposal) && <a className="text-primary underline" href={`${base}/dispatch?id=${proposal}`}>{t("conversationOpenProposal")}</a>}
    {typeof work === "string" && /^[1-9][0-9]{0,18}$/.test(work) && <a className="text-primary underline" href={`${base}/work-items?work=${work}`}>{t("conversationOpenWork")}</a>}
    {(message.kind === "DECISION_CARD" || message.kind === "DISPATCH_CARD") && <p className="text-sm text-muted-foreground">{t("conversationCardAuthority")}</p>}
  </section>
}
