import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlProvider } from "use-intl"

import { useAutoForm } from "./use-auto-form"
import { requestContract } from "./contract.test"
import messages from "../../i18n/messages/en-US.json"
import type { UseAutoFormOptions, UseAutoFormReturn } from "./types"
import { isRequired } from "./types"

function formFor(options: UseAutoFormOptions): UseAutoFormReturn {
  let form!: UseAutoFormReturn
  function Probe() {
    form = useAutoForm(options)
    return null
  }
  renderToStaticMarkup(
    <IntlProvider locale="en-US" timeZone="UTC" messages={messages}>
      <Probe />
    </IntlProvider>
  )
  return form
}

describe("AutoForm submission boundary", () => {
  it("marks a populated PATCH field that cannot be cleared without making omission invalid", async () => {
    const contract = requestContract({
      name: { type: "string", minLength: 1 },
      description: { type: "string" },
    })
    contract.method = "PATCH"
    let submitted: unknown
    const fields = {
      name: { name: "Name", type: "text" as const },
      description: { name: "Description", type: "text" as const },
    }
    const populated = formFor({
      fields,
      contract,
      mode: "edit",
      initialData: { name: "Existing", description: "" },
    })
    expect(isRequired(populated.fields.name!, populated.values)).toBe(true)
    expect(isRequired(populated.fields.description!, populated.values)).toBe(
      false
    )
    const omitted = formFor({
      fields,
      contract,
      onSubmit: (data) => {
        submitted = data
      },
    })
    expect(isRequired(omitted.fields.name!, omitted.values)).toBe(false)
    await omitted.handleSubmit()
    expect(submitted).toEqual({ name: undefined, description: undefined })
  })

  it.each(["add", "edit", "copy"] as const)(
    "explicit readonly also blocks %s submission",
    async (mode) => {
      let calls = 0
      const form = formFor({
        mode,
        readonly: true,
        fields: { title: { name: "Title", type: "text", value: "Value" } },
        onSubmit: () => {
          calls++
        },
      })
      await form.handleSubmit()
      expect(calls).toBe(0)
    }
  )

  it("preserves explicit null rather than substituting the create default", () => {
    const form = formFor({
      mode: "edit",
      fields: {
        deadline: { name: "Deadline", type: "date", value: "2026-10-15" },
      },
      initialData: { deadline: null },
    })
    expect(form.getValues()).toEqual({ deadline: null })
  })

  it("accepts only one submission while asynchronous validation is pending", async () => {
    let release!: () => void
    const pending = new Promise<void>((resolve) => {
      release = resolve
    })
    let calls = 0
    const form = formFor({
      fields: {
        title: {
          name: "Title",
          type: "text",
          value: "Value",
          config: {
            rules: [
              async () => {
                await pending
                return undefined
              },
            ],
          },
        },
      },
      onSubmit: () => {
        calls++
      },
    })
    const first = form.handleSubmit()
    const duplicate = form.handleSubmit()
    release()
    await Promise.all([first, duplicate])
    expect(calls).toBe(1)
    await form.handleSubmit()
    expect(calls).toBe(2)
  })

  it("releases the submission lock and reports validation failures", async () => {
    const failure = new Error("Validation service unavailable")
    let attempts = 0
    const failures: unknown[] = []
    const form = formFor({
      fields: {
        title: {
          name: "Title",
          type: "text",
          config: {
            rules: [
              () => {
                attempts++
                throw failure
              },
            ],
          },
        },
      },
      onSubmit: () => {
        throw new Error("Must not submit")
      },
      onError: (error) => {
        failures.push(error)
      },
    })
    await form.handleSubmit()
    await form.handleSubmit()
    expect(attempts).toBe(2)
    expect(failures).toEqual([failure, failure])
  })

  it("detail never validates or submits even when its handler is invoked directly", async () => {
    let validations = 0
    let submissions = 0
    let prevented = false
    const form = formFor({
      mode: "detail",
      fields: {
        title: {
          name: "Title",
          type: "text",
          config: {
            rules: [
              () => {
                validations++
                return undefined
              },
            ],
          },
        },
      },
      initialData: { title: "Published record" },
      onSubmit: () => {
        submissions++
      },
    })
    await form.handleSubmit({
      preventDefault() {
        prevented = true
      },
    } as React.FormEvent)
    expect(prevented).toBe(true)
    expect(validations).toBe(0)
    expect(submissions).toBe(0)
    expect(form.getValues()).toEqual({ title: "Published record" })
  })

  it.each(["add", "edit", "copy"] as const)(
    "%s still submits its own values",
    async (mode) => {
      const submitted: Record<string, unknown>[] = []
      const form = formFor({
        mode,
        fields: {
          title: { name: "Title", type: "text", value: "New record" },
          code: { name: "Code", type: "text", value: "", copy: false },
          serverOnly: { name: "Server", type: "text", submit: false },
        },
        initialData: {
          title: "Existing record",
          code: "unique",
          serverOnly: "keep",
        },
        onSubmit: (values) => {
          submitted.push(values)
        },
      })
      await form.handleSubmit()
      expect(submitted).toEqual([
        {
          title: mode === "add" ? "New record" : "Existing record",
          code: mode === "edit" ? "unique" : "",
        },
      ])
    }
  )
})

describe("AutoForm operation contract binding", () => {
  it("enforces byte rules at submission without trimming the request payload", async () => {
    for (const value of [" 中 ", "中文", "\u0085"]) {
      const submitted: unknown[] = []
      const form = formFor({
        fields: { value: { name: "Value", type: "text", value } },
        contract: requestContract({
          value: {
            type: "string",
            "x-field-rules": [
              {
                rule: "utf8-bytes",
                args: { min: 1, max: 3, normalize: "trim-space" },
              },
            ],
          },
        }),
        onSubmit: (data) => {
          submitted.push(data.value)
        },
      })
      await form.handleSubmit()
      expect(submitted).toEqual(value === " 中 " ? [value] : [])
      expect(form.getValues()).toEqual({ value })
    }
  })
  it("uses backend rules and submits parsed numbers without mutating editing values", async () => {
    let submitted: Record<string, unknown> | undefined
    const form = formFor({
      fields: { amount: { name: "Amount", type: "number", value: "2500" } },
      contract: requestContract({ amount: { type: "integer", minimum: 1 } }, [
        "amount",
      ]),
      onSubmit: (data) => {
        submitted = data
      },
    })
    expect(form.fields.amount.required).toBe(true)
    await form.handleSubmit()
    expect(submitted).toEqual({ amount: 2500 })
    expect(form.values.amount).toBe("2500")
  })
  it("blocks missing metadata, incompatible rules, and invalid dates", async () => {
    for (const contract of [
      null,
      requestContract({ value: { type: "string", pattern: "(?i)bad" } }),
      requestContract({ value: { type: "string", format: "date" } }),
    ]) {
      let calls = 0
      const form = formFor({
        fields: { value: { name: "Value", type: "text", value: "2025-02-29" } },
        contract,
        onSubmit: () => {
          calls++
        },
      })
      await form.handleSubmit()
      expect(calls).toBe(0)
    }
  })
})

describe("operation required preserves JSON Schema presence semantics", () => {
  it.each([
    ["empty string", "", { type: "string" }],
    ["whitespace", "   ", { type: "string" }],
    ["empty array", [], { type: "array", items: { type: "string" } }],
    ["nullable value", null, { type: ["string", "null"] }],
    ["false", false, { type: "boolean" }],
    ["zero", 0, { type: "integer" }],
  ])(
    "allows present %s when the schema permits it",
    async (_label, value, property) => {
      const submitted: unknown[] = []
      const form = formFor({
        fields: { value: { name: "Value", type: "text", value } },
        contract: requestContract({ value: property }, ["value"]),
        onSubmit: (data) => {
          submitted.push(data.value)
        },
      })
      await form.handleSubmit()
      expect(submitted).toEqual([value])
    }
  )

  it("still refuses an absent required property", async () => {
    let submitted = false
    const form = formFor({
      fields: { value: { name: "Value", type: "text" } },
      contract: requestContract({ value: { type: "string" } }, ["value"]),
      onSubmit: () => {
        submitted = true
      },
    })
    await form.handleSubmit()
    expect(submitted).toBe(false)
  })

  it("preserves explicitly configured legacy non-empty validation", async () => {
    let submitted = false
    const form = formFor({
      fields: {
        value: { name: "Value", type: "text", value: "", required: true },
      },
      contract: requestContract({ value: { type: "string" } }, ["value"]),
      onSubmit: () => {
        submitted = true
      },
    })
    await form.handleSubmit()
    expect(submitted).toBe(false)
  })
})
