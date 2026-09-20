import {
  audioPlugin,
  fallbackPlugin,
  imagePlugin,
  officePlugin,
  pdfPlugin,
  textPlugin,
  videoPlugin,
  type PreviewCommand,
  type PreviewPlugin,
} from "@open-file-viewer/core"
import "@open-file-viewer/core/style.css"
import { FileViewer } from "@open-file-viewer/react"
import pdfLegacyWorkerSrc from "pdfjs-dist/legacy/build/pdf.worker.mjs?url"
import pdfWorkerSrc from "pdfjs-dist/build/pdf.worker.mjs?url"
import { normalizeOfficeEmbeddedImageURL } from "../lib/office-preview-safety"
import {
  officePreviewFit,
  presentationPageLabel,
} from "../lib/office-preview"

const previewPlugins = (presentationNavigationLabel: string) => [
  imagePlugin(),
  textPlugin(),
  pdfPlugin({ workerSrc: pdfWorkerSrc, legacyWorkerSrc: pdfLegacyWorkerSrc }),
  videoPlugin(),
  audioPlugin(),
  sanitizedOfficePlugin(presentationNavigationLabel),
  fallbackPlugin(),
]

const safeOfficeExtensions = new Set(["docx", "pptx", "xlsx"])
const safeOfficeMimeTypes = new Set([
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
])

const officeZoomMinimum = 0.5
const officeZoomMaximum = 6
const officeZoomStep = 0.25

function sanitizedOfficePlugin(presentationNavigationLabel: string): PreviewPlugin {
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
      let zoom = 1
      const applyZoom = () => {
        const panel = staging.querySelector<HTMLElement>(".ofv-office")
        panel?.style.setProperty("--ofv-office-zoom", String(zoom))
        panel?.dispatchEvent(new CustomEvent("ofv-office-zoom"))
        staging
          .querySelectorAll<HTMLElement>(
            ".ofv-pptx-viewer > div[data-slide-index], .ofv-ppt-binary-slide"
          )
          .forEach((element) => {
            element.style.zoom = zoom === 1 ? "" : String(zoom)
            if (element.matches(".ofv-pptx-viewer > div[data-slide-index]")) {
              element.style.width = zoom === 1 ? "" : "max-content"
              element.style.maxWidth = zoom === 1 ? "" : "none"
              element.style.overflow = zoom === 1 ? "" : "visible"
            }
          })
        context.toolbar?.setZoom(zoom)
      }
      const disposePresentationNavigation = addPresentationNavigation(
        staging,
        context.options.locale === "zh-CN" ? "zh-CN" : "en-US",
        presentationNavigationLabel,
        (page) => instance.goToPage?.(page) ?? false
      )
      applyZoom()
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
        canCommand(command: PreviewCommand) {
          return (
            command === "zoom-in" ||
            command === "zoom-out" ||
            command === "zoom-reset" ||
            instance.canCommand?.(command) === true
          )
        },
        command(command: PreviewCommand) {
          if (command === "zoom-reset") zoom = 1
          else if (command === "zoom-in")
            zoom = Math.min(officeZoomMaximum, zoom + officeZoomStep)
          else if (command === "zoom-out")
            zoom = Math.max(officeZoomMinimum, zoom - officeZoomStep)
          else return instance.command?.(command)
          applyZoom()
          return true
        },
        destroy() {
          observer.disconnect()
          disposePresentationNavigation()
          instance.destroy()
          staging.remove()
        },
      }
    },
  }
}

function addPresentationNavigation(
  root: HTMLElement,
  locale: "zh-CN" | "en-US",
  navigationLabel: string,
  goToPage: (page: number) => boolean
) {
  const viewer = root.querySelector<HTMLElement>(".ofv-pptx-viewer")
  const slides = viewer
    ? [...viewer.querySelectorAll<HTMLElement>(":scope > div[data-slide-index]")]
    : []
  if (!viewer || slides.length < 2) return () => undefined

  const shell = document.createElement("div")
  shell.className = "iam-presentation-preview"
  const navigation = document.createElement("nav")
  navigation.className = "iam-presentation-pages"
  navigation.setAttribute("aria-label", navigationLabel)
  let selectedPage = 1
  const applySelectedPage = () => {
    viewer
      .querySelectorAll<HTMLElement>(":scope > div[data-slide-index]")
      .forEach((candidate, index) => {
        candidate.dataset.iamPresentationActive =
          index + 1 === selectedPage ? "true" : "false"
      })
  }
  const buttons = slides.map((slide, index) => {
    const page = index + 1
    const title = (slide.textContent ?? "")
      .replace(/([a-z0-9])([A-Z])/gu, "$1 $2")
      .replace(/\s+/gu, " ")
      .trim()
      .slice(0, 80)
    const button = document.createElement("button")
    button.type = "button"
    button.className = "iam-presentation-page"
    button.setAttribute("aria-current", page === 1 ? "page" : "false")
    button.textContent = presentationPageLabel(page, title, locale)
    button.addEventListener("click", () => {
      selectedPage = page
      goToPage(page)
      applySelectedPage()
      requestAnimationFrame(applySelectedPage)
      buttons.forEach((candidate) =>
        candidate.setAttribute(
          "aria-current",
          candidate === button ? "page" : "false"
        )
      )
    })
    navigation.append(button)
    return button
  })
  applySelectedPage()
  const slideObserver = new MutationObserver(applySelectedPage)
  slideObserver.observe(viewer, { childList: true })
  viewer.before(shell)
  shell.append(navigation, viewer)
  return () => {
    slideObserver.disconnect()
    viewer
      .querySelectorAll<HTMLElement>(":scope > div[data-slide-index]")
      .forEach((slide) => {
      delete slide.dataset.iamPresentationActive
      })
    shell.replaceWith(viewer)
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
  presentationNavigationLabel,
}: {
  blob: Blob
  name: string
  mimeType: string
  locale: "zh-CN" | "en-US"
  presentationNavigationLabel: string
}) {
  return (
    <FileViewer
      file={blob}
      fileName={name}
      mimeType={mimeType}
      width="100%"
      height="100%"
      fit={officePreviewFit(name, mimeType)}
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
      plugins={previewPlugins(presentationNavigationLabel)}
    />
  )
}
