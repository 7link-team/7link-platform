// Isolated browser boundary fixture; never imported by the production router.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { iamApi, type KnowledgeEntry } from "../../lib/iam-api"
import { loadMessages } from "../../lib/i18n"
import KnowledgePage from "./page"
import ConversationsPage from "../conversations/page"
import "../../index.css"

const params = new URLSearchParams(location.search)
const locale = params.get("locale") ?? "en-US"
const mode = params.get("mode") ?? "knowledge"
const entry: KnowledgeEntry = {
  id: "21",
  scope: "PROJECT",
  project_id: "3",
  kind: "DOCUMENT",
  state: "ACTIVE",
  origin: "HUMAN",
  risk_flags: [],
  ttl_days: 180,
  use_count: 0,
  title: "Deployment guide",
  body: "Review the release checklist before deployment.",
  provenance: "Human-maintained notes",
  content_hash: "0".repeat(64),
  version: 1,
  created_by: "2",
  created_at: "2026-10-20T00:00:00Z",
}
iamApi.knowledgeEntries = async (_tenant, query) => {
  if (query.scope === "AGENT")
    throw new Error("Access denied to this Agent memory.")
  return {
    items: query.scope === "PROJECT" && query.state === "ACTIVE" ? [entry] : [],
    has_more: false,
  }
}
iamApi.conversationChannels = async () => ({
  items: [
    {
      id: "31",
      name: "Project discussion",
      kind: "PROJECT_DEFAULT",
      visibility: "CLIENT_SHARED",
      topic: "",
      mention_unread: 0,
      unread: 1,
      version: 1,
    },
  ],
  has_more: false,
})
iamApi.conversationMembers = async () => ({
  items: [
    {
      principal_id: "2",
      member_kind: "HUMAN",
      role: "MEMBER",
      external: false,
      joined_at: "2026-10-20T00:00:00Z",
    },
  ],
  has_more: false,
})
iamApi.conversationMessages = async () => ({
  items: [
    {
      id: "41",
      channel_id: "31",
      seq: 1,
      stream_seq: 1,
      author_id: "2",
      kind: "TEXT",
      attention: false,
      body: "**Release notes**\n<script>alert('unsafe')</script>",
      edited: false,
      retracted: false,
      visibility: "CHANNEL",
      version: 1,
      created_at: "2026-10-20T00:00:00Z",
      mentions: [],
      attachments: [{ id: "71", file_id: "51", version_id: "61" }],
      saved: false,
    },
  ],
  has_more: false,
})
iamApi.conversationAttachmentCandidate = async (_channel, file) => ({
  space_id: "11",
  file_id: file,
  version_id: "61",
  expected_file_version: 1,
  name: "release-notes.md",
  mime: "text/markdown",
  size: 6,
})
iamApi.downloadConversationAttachment = async () =>
  new Blob(["pinned"], { type: "text/markdown" })

iamApi.requestContract = async (route) => ({
  version: "fixture-v1",
  operation_id: "channel.message_convert",
  method: route.method,
  path: route.url,
  request_body: {
    content: {
      "application/json": {
        schema: {
          type: "object",
          required: [
            "title",
            "objective",
            "accountable_principal_id",
            "criteria",
          ],
          properties: {
            title: { type: "string", minLength: 1, maxLength: 200 },
            objective: { type: "string", minLength: 1, maxLength: 4000 },
            accountable_principal_id: {
              type: "string",
              pattern: "^[1-9][0-9]*$",
            },
            criteria: {
              type: "array",
              minItems: 1,
              maxItems: 50,
              items: {
                type: "object",
                required: ["text", "verification_method"],
                properties: {
                  text: { type: "string", minLength: 1, maxLength: 2000 },
                  verification_method: {
                    type: "string",
                    minLength: 1,
                    maxLength: 2000,
                  },
                },
              },
            },
          },
        },
      },
    },
  },
  schemas: {},
})
const conversionAttempts: { body: unknown; key: string }[] = []
iamApi.convertConversationMessage = async (_channel, _message, body, key) => {
  conversionAttempts.push({ body, key })
  ;(window as unknown as { conversionAttempts: unknown }).conversionAttempts =
    conversionAttempts
  if (conversionAttempts.length === 1)
    throw new Error("Temporary failure; retry")
  return { work_item_id: "81", message_id: "41", project_id: "3" }
}
iamApi.postConversationMessage = async (_channel, body) => {
  ;(window as unknown as { posted: unknown }).posted = body
  return {
    id: "42",
    channel_id: "31",
    seq: 2,
    stream_seq: 2,
    author_id: "2",
    kind: "TEXT",
    attention: false,
    body: body.body,
    edited: false,
    retracted: false,
    visibility: body.visibility,
    version: 1,
    created_at: "2026-10-20T00:00:00Z",
    attachments: [],
    mentions: [],
    saved: false,
  }
}
iamApi.readConversation = async (_channel, seq) => seq
iamApi.saveConversationMessage = async (_channel, _message, saved) => saved
const router = createMemoryRouter(
  [
    {
      element: (
        <Outlet
          context={{
            permissions: new Set(["channel_post", "work_item_write"]),
            permissionsResolved: true,
          }}
        />
      ),
      children: [
        {
          path: "/admin/:tenantID/entities/:entityID/knowledge",
          element: <KnowledgePage />,
        },
        {
          path: "/admin/:tenantID/entities/:entityID/conversations",
          element: <ConversationsPage />,
        },
      ],
    },
  ],
  {
    initialEntries: [
      `/admin/1/entities/3/${mode === "conversations" ? "conversations" : "knowledge"}`,
    ],
  }
)
void loadMessages(locale).then((messages) =>
  createRoot(document.getElementById("root")!).render(
    <IntlAppProvider initialLocale={locale} initialMessages={messages}>
      <main className="mx-auto max-w-6xl p-4">
        <RouterProvider router={router} />
      </main>
    </IntlAppProvider>
  )
)
