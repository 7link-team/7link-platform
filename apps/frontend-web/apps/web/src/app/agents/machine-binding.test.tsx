import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { ApiError } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { AgentMachineBinding } from "./machine-binding"
import {
  rebindFailureDetails,
  rebindObservations,
} from "./machine-binding-capabilities"

describe("Agent machine binding", () => {
  it("preserves exact unknown MCP details only for the matching business failure", () => {
    expect(
      rebindFailureDetails(
        new ApiError(
          422,
          "invalid",
          "invalid",
          { unknown: ["Reader", "writer", 42] },
          142007
        )
      )
    ).toEqual(["Reader", "writer"])
    expect(
      rebindFailureDetails(
        new ApiError(422, "invalid", "invalid", { unknown: ["reader"] }, 142026)
      )
    ).toEqual([])
    expect(rebindFailureDetails(new Error("offline"))).toEqual([])
  })
  it("shows home-scope evidence without substituting another workdir's MCP", () => {
    const capabilities = {
      discoveries: [
        {
          scope_key: "home",
          kind: "MODELS",
          discovery: [
            {
              runtime: "CODEX",
              source: "CLI_CACHE",
              state: "READY",
              reasoning: [{ model: "gpt-model", efforts: ["high"] }],
            },
          ],
        },
        {
          scope_key: "home",
          kind: "CAPABILITIES",
          discovery: [
            {
              runtime: "CODEX",
              source: "CACHED",
              state: "ERROR",
              mcp: ["home-reader"],
            },
          ],
        },
        {
          scope_key: "42",
          kind: "CAPABILITIES",
          discovery: [
            { runtime: "CODEX", state: "READY", mcp: ["project-reader"] },
          ],
        },
      ],
    } as unknown as components["schemas"]["MachineCapabilities"]
    const facts = rebindObservations(capabilities)
    expect(facts).toHaveLength(2)
    expect(facts[0]?.reasoning).toEqual([
      { model: "gpt-model", efforts: ["high"] },
    ])
    expect(facts[1]?.mcp).toEqual(["home-reader"])
    expect(facts[1]?.state).toBe("ERROR")
  })
  it("handles nullable reasoning capabilities without inventing effort support", () => {
    const capabilities = {
      discoveries: [
        {
          scope_key: "home",
          kind: "MODELS",
          discovery: [
            { runtime: "CODEX", reasoning: null },
            {
              runtime: "CODEX",
              reasoning: [{ model: "gpt-model", efforts: null }],
            },
          ],
        },
      ],
    } as unknown as components["schemas"]["MachineCapabilities"]
    const facts = rebindObservations(capabilities)
    expect(facts[0]?.reasoning).toEqual([])
    expect(facts[1]?.reasoning).toEqual([{ model: "gpt-model", efforts: [] }])
    expect(facts[1]?.reasoning[0]?.efforts.join(", ")).toBe("")
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`explains pending Runner confirmation without fabricated receipts: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const strings = messages.iam as Record<string, string>
      const html = renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={messages}>
          <MemoryRouter>
            <AgentMachineBinding tenant="7" agent="21" />
          </MemoryRouter>
        </IntlAppProvider>
      )
      expect(html).toContain(strings.agentMachineBinding!)
      expect(html).toContain(strings.agentRebindHelp!)
      expect(html).not.toContain(strings.agentBindingStatus_DELIVERED!)
      expect(html).not.toContain('type="submit"')
      expect(html).not.toContain("client_secret")
    })
  }
})
