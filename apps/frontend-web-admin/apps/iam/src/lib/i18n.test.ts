import { describe, expect, it } from "bun:test"
import { readFileSync, readdirSync } from "node:fs"
import ts from "typescript"
import { locales, localeNames } from "@workspace/ui/i18n/config"
import { loadMessages } from "./i18n"
import zh from "../messages/zh-CN.json"
import en from "../messages/en-US.json"
import ms from "../messages/ms-MY.json"

describe("IAM translations", () => {
  it("uses language-only display names without changing canonical locale codes", () => {
    expect(localeNames).toEqual({
      "en-US": "English",
      "zh-CN": "简体中文",
      "ms-MY": "Bahasa Melayu",
    })
    expect(Object.keys(localeNames)).toEqual([...locales])
  })
  it("provides every message and interpolation in all supported languages", () => {
    for (const messages of [en, ms]) {
      expect(Object.keys(messages).sort()).toEqual(Object.keys(zh).sort())
      for (const key of Object.keys(zh) as (keyof typeof zh)[]) {
        expect(messages[key].trim().length).toBeGreaterThan(0)
        expect(messages[key]).not.toMatch(/\p{Script=Han}/u)
        expect(messages[key].match(/\{\w+\}/g)?.sort() ?? []).toEqual(
          zh[key].match(/\{\w+\}/g)?.sort() ?? []
        )
      }
    }
  })
  it("loads shared and application messages together for initial load and switching", async () => {
    for (const locale of locales) {
      const messages = await loadMessages(locale)
      expect(messages.localeSwitcher).toBeDefined()
      expect(messages.iam).toBeDefined()
    }
  })
  it("does not leave hardcoded Chinese in any IAM view or component", () => {
    const root = new URL("../", import.meta.url)
    for (const file of readdirSync(root, {
      recursive: true,
      encoding: "utf8",
    }).filter((file) => file.endsWith(".tsx") && !file.endsWith(".test.tsx"))) {
      const source = readFileSync(
        new URL(`../${file}`, import.meta.url),
        "utf8"
      )
      const parsed = ts.createSourceFile(
        file,
        source,
        ts.ScriptTarget.Latest,
        true,
        ts.ScriptKind.TSX
      )
      const inspect = (node: ts.Node) => {
        if (
          ts.isStringLiteral(node) ||
          ts.isJsxText(node) ||
          ts.isTemplateHead(node) ||
          ts.isTemplateMiddle(node) ||
          ts.isTemplateTail(node) ||
          ts.isNoSubstitutionTemplateLiteral(node)
        ) {
          expect(node.text).not.toMatch(/\p{Script=Han}/u)
        }
        ts.forEachChild(node, inspect)
      }
      inspect(parsed)
      for (const match of source.matchAll(/\bt\("([\w]+)"/g))
        expect(zh).toHaveProperty(match[1]!)
    }
  })
  it("does not silently overwrite duplicate message keys", () => {
    for (const locale of locales) {
      const raw = readFileSync(
        new URL(`../messages/${locale}.json`, import.meta.url),
        "utf8"
      )
      const parsed = ts.parseJsonText(`${locale}.json`, raw)
      const statement = parsed.statements[0]!
      expect(ts.isExpressionStatement(statement)).toBe(true)
      if (
        !ts.isExpressionStatement(statement) ||
        !ts.isObjectLiteralExpression(statement.expression)
      )
        throw new Error("Expected a message object")
      const names = statement.expression.properties.map((property) =>
        property.name?.getText(parsed)
      )
      expect(new Set(names).size).toBe(names.length)
    }
  })
})
