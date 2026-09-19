import {
  audioPlugin,
  fallbackPlugin,
  imagePlugin,
  officePlugin,
  pdfPlugin,
  textPlugin,
  videoPlugin,
  type PreviewPlugin,
} from "@open-file-viewer/core"
import "@open-file-viewer/core/style.css"
import { FileViewer } from "@open-file-viewer/react"
import pdfLegacyWorkerSrc from "pdfjs-dist/legacy/build/pdf.worker.mjs?url"
import pdfWorkerSrc from "pdfjs-dist/build/pdf.worker.mjs?url"
import { normalizeOfficeEmbeddedImageURL } from "../lib/office-preview-safety"

const plugins = [
  imagePlugin(),
  textPlugin(),
  pdfPlugin({ workerSrc: pdfWorkerSrc, legacyWorkerSrc: pdfLegacyWorkerSrc }),
  videoPlugin(),
  audioPlugin(),
  sanitizedOfficePlugin(),
  fallbackPlugin(),
]

const safeOfficeExtensions = new Set(["docx", "pptx"])
const safeOfficeMimeTypes = new Set([
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
])

function sanitizedOfficePlugin(): PreviewPlugin {
  const office = officePlugin()
  return {
    name: "sanitized-office",
    match(file) {
      return (
        safeOfficeExtensions.has(file.extension) ||
        safeOfficeMimeTypes.has(file.mimeType)
      )
    },
    async render(context) {
      const staging = document.createElement("div")
      staging.className = "size-full"
      const instance = await office.render({ ...context, viewport: staging })
      sanitizeOfficePreview(staging)
      const observer = new MutationObserver(() =>
        sanitizeOfficePreview(staging)
      )
      observer.observe(staging, {
        attributes: true,
        childList: true,
        subtree: true,
      })
      context.viewport.append(staging)
      return {
        ...instance,
        destroy() {
          observer.disconnect()
          instance.destroy()
          staging.remove()
        },
      }
    },
  }
}

function sanitizeOfficePreview(root: HTMLElement) {
  root
    .querySelectorAll("iframe,script,object,embed,form,base,meta,link")
    .forEach((element) => element.remove())
  root.querySelectorAll<HTMLElement>("*").forEach((element) => {
    for (const attribute of [...element.attributes]) {
      const name = attribute.name.toLowerCase()
      if (name.startsWith("on") || name === "srcdoc" || name === "formaction")
        element.removeAttribute(attribute.name)
    }
    sanitizeOfficeURL(element, "href", true)
    sanitizeOfficeURL(element, "src", false)
    sanitizeOfficeURL(element, "xlink:href", false)
    const inlineStyle = element.getAttribute("style")
    if (inlineStyle) {
      const sanitized = sanitizeOfficeCSS(inlineStyle)
      if (sanitized !== inlineStyle) element.setAttribute("style", sanitized)
    }
  })
  document
    .querySelectorAll<HTMLStyleElement>("style.ofv-docx-style-container")
    .forEach((style) => {
      const current = style.textContent ?? ""
      const sanitized = sanitizeOfficeCSS(current)
      if (current !== sanitized) style.textContent = sanitized
    })
}

function sanitizeOfficeURL(
  element: HTMLElement,
  attribute: "href" | "src" | "xlink:href",
  allowNavigation: boolean
) {
  const value = element.getAttribute(attribute)?.trim()
  if (!value) return
  if (attribute === "src" && element instanceof HTMLImageElement) {
    const normalized = normalizeOfficeEmbeddedImageURL(value)
    if (normalized) {
      element.setAttribute(attribute, normalized)
      return
    }
  }
  const lower = value.toLowerCase()
  const allowed = allowNavigation
    ? lower.startsWith("#") ||
      lower.startsWith("https://") ||
      lower.startsWith("http://") ||
      lower.startsWith("mailto:")
    : lower.startsWith("blob:") ||
      lower.startsWith("data:image/") ||
      lower.startsWith("data:font/") ||
      lower.startsWith("data:application/font")
  if (!allowed) {
    element.removeAttribute(attribute)
    return
  }
  if (allowNavigation && !lower.startsWith("#")) {
    if (element.getAttribute("target") !== "_blank")
      element.setAttribute("target", "_blank")
    if (element.getAttribute("rel") !== "noopener noreferrer")
      element.setAttribute("rel", "noopener noreferrer")
  }
}

function sanitizeOfficeCSS(value: string) {
  return value
    .replace(/@import\s+[^;]+;?/giu, "")
    .replace(
      /url\(\s*(['"]?)(?!data:(?:image|font|application\/font)|blob:)[^)]+\1\s*\)/giu,
      "none"
    )
}

export default function OpenFileViewerRenderer({
  blob,
  name,
  mimeType,
  locale,
}: {
  blob: Blob
  name: string
  mimeType: string
  locale: "zh-CN" | "en-US"
}) {
  return (
    <FileViewer
      file={blob}
      fileName={name}
      mimeType={mimeType}
      width="100%"
      height="100%"
      fit="contain"
      locale={locale}
      theme="auto"
      toolbar={{
        zoom: true,
        rotate: true,
        download: true,
        fullscreen: true,
        print: true,
        search: true,
      }}
      plugins={plugins}
    />
  )
}
