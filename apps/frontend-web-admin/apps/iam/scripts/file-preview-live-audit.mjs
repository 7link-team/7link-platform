import assert from "node:assert/strict"
import { mkdirSync, readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const webURL = process.env.IAM_WEB_URL ?? "http://127.0.0.1:18291"
const passwordFile = new URL(
  "../../../../backend-api/.temp/iam-uat/admin-password",
  import.meta.url
)
const password = readFileSync(passwordFile, "utf8").trim()
const runID = Date.now().toString(36)
const names = {
  text: `preview-${runID}.txt`,
  image: `preview-${runID}.png`,
  pdf: `preview-${runID}.pdf`,
  office: `preview-${runID}.docx`,
}
const textBody = `SevenLink preview acceptance ${runID}`
const screenshots = new URL("../../../../../.local/screenshots/file-preview/", import.meta.url)
mkdirSync(screenshots, { recursive: true })

function makePDF(message) {
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
    `<< /Length ${message.length + 35} >>\nstream\nBT /F1 18 Tf 72 720 Td (${message}) Tj ET\nendstream`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
  ]
  let body = "%PDF-1.4\n"
  const offsets = [0]
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(body))
    body += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`
  }
  const xref = Buffer.byteLength(body)
  body += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  body += offsets
    .slice(1)
    .map((offset) => `${String(offset).padStart(10, "0")} 00000 n \n`)
    .join("")
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(body)
}

const files = [
  { name: names.text, mimeType: "text/plain", buffer: Buffer.from(textBody) },
  {
    name: names.image,
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFElEQVR4nGP4z8DAwMDAxMDAwMAAAAwBAQDJ/pLvAAAAAElFTkSuQmCC",
      "base64"
    ),
  },
  { name: names.pdf, mimeType: "application/pdf", buffer: makePDF("SevenLink preview") },
  {
    name: names.office,
    mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    buffer: Buffer.concat([
      Buffer.from([0x50, 0x4b, 0x03, 0x04]),
      Buffer.from("container signature only; never parsed during safe fallback"),
    ]),
  },
]

const browser = await chromium.launch({ channel: "chrome", headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
const consoleErrors = []
const failedResponses = []
const loadedAssets = []
const rawRequests = []
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text())
})
page.on("pageerror", (error) => consoleErrors.push(error.message))
page.on("response", (response) => {
  const url = response.url()
  loadedAssets.push(url)
  if (response.status() >= 400 && !url.includes("/authn/sessions/current"))
    failedResponses.push(`${response.status()} ${url}`)
})
page.on("request", (request) => {
  if (/\/api\/files\/[^/]+\/raw(?:\?|$)/u.test(request.url()))
    rawRequests.push({ url: request.url(), headers: request.headers() })
})

async function assertFits() {
  const size = await page.evaluate(() => ({
    width: document.documentElement.scrollWidth,
    viewport: document.documentElement.clientWidth,
  }))
  assert(size.width <= size.viewport + 1, `horizontal overflow: ${JSON.stringify(size)}`)
}

function fileRow(name) {
  return page.getByText(name, { exact: true }).locator("xpath=ancestor::*[@data-file-id][1]")
}

async function closePreview() {
  await page.keyboard.press("Escape")
  await page.getByRole("dialog").waitFor({ state: "hidden" })
}

try {
  await page.goto(`${webURL}/login`)
  await page.locator('input[name="login_name"]').fill("admin@example.test")
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await page.waitForTimeout(1_000)
  if (new URL(page.url()).pathname === "/login") {
    const state = await page.locator("main").innerText()
    throw new Error(
      `Login did not complete: ${state}; responses=${failedResponses.join(", ")}`
    )
  }
  consoleErrors.length = 0
  failedResponses.length = 0
  await page.goto(`${webURL}/account/files`)

  const createRoot = page.getByRole("button", { name: "Create personal files", exact: true })
  if (await createRoot.isVisible().catch(() => false)) await createRoot.click()
  const upload = page.locator('input[type="file"][aria-label="Upload file"]')
  await upload.waitFor({ state: "attached" })
  assert.equal(
    loadedAssets.some((url) => url.includes("open-file-viewer-renderer")),
    false,
    "viewer chunk loaded before preview"
  )
  await upload.setInputFiles(files)
  for (const name of Object.values(names))
    await page.getByText(name, { exact: true }).waitFor({ timeout: 15_000 })
  await fileRow(names.image)
    .locator('[data-file-thumbnail="ready"] img')
    .waitFor({ timeout: 15_000 })

  const expectedCategories = new Map([
    [names.text, "text"],
    [names.image, "image"],
    [names.pdf, "pdf"],
    [names.office, "document"],
  ])
  for (const [name, category] of expectedCategories)
    assert.equal(
      await fileRow(name).locator(`[data-file-category="${category}"]`).count(),
      1,
      `${name} did not render a ${category} icon`
    )
  for (const view of ["Small icons", "Large icons", "Details view"]) {
    await page.getByRole("button", { name: view, exact: true }).click()
    for (const [name, category] of expectedCategories)
      assert.equal(
        await fileRow(name).locator(`[data-file-category="${category}"]`).count(),
        1,
        `${name} lost its ${category} icon in ${view}`
      )
    assert.equal(
      await fileRow(names.image).locator('[data-file-thumbnail="ready"] img').count(),
      1,
      `${names.image} lost its thumbnail in ${view}`
    )
  }

  const directorySurface = page.locator('[aria-label="Current folder menu"]')
  const createBlankFile = async (menuName, fileName) => {
    const box = await directorySurface.boundingBox()
    assert(box, "current folder surface is missing")
    await directorySurface.click({
      button: "right",
      position: { x: 8, y: Math.max(8, box.height - 8) },
    })
    const createNew = page.getByRole("menuitem", { name: "New", exact: true })
    await createNew.hover()
    await page.getByRole("menuitem", { name: menuName, exact: true }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("textbox", { name: "Name", exact: true }).fill(fileName)
    await dialog.getByRole("button", { name: "Save", exact: true }).click()
    await page.getByText(fileName, { exact: true }).waitFor()
  }
  await createBlankFile("Text document", `preview-${runID}-created.txt`)
  await createBlankFile("Markdown document", `preview-${runID}-created.md`)

  await page.getByText(names.text, { exact: true }).click()
  const textDialog = page.getByRole("dialog")
  await textDialog.getByText(textBody, { exact: false }).waitFor({ timeout: 15_000 })
  await closePreview()

  await page.getByText(names.image, { exact: true }).click()
  const imageDialog = page.getByRole("dialog")
  await imageDialog.locator("img").first().waitFor({ timeout: 15_000 })
  await closePreview()

  await page.getByText(names.pdf, { exact: true }).click()
  const pdfDialog = page.getByRole("dialog")
  const pdfCanvas = pdfDialog.locator("canvas").first()
  await pdfCanvas.waitFor({ timeout: 20_000 })
  const canvasSize = await pdfCanvas.evaluate((canvas) => ({
    width: canvas.width,
    height: canvas.height,
  }))
  assert(canvasSize.width > 0 && canvasSize.height > 0, "PDF canvas is blank")
  await page.screenshot({
    path: fileURLToPath(new URL("desktop-pdf.png", screenshots)),
    fullPage: true,
  })
  await closePreview()

  const rawCountBeforeOffice = rawRequests.length
  await page.getByText(names.office, { exact: true }).click()
  await page
    .getByRole("dialog")
    .getByText("Preview is not available for this file type.", { exact: true })
    .waitFor()
  assert.equal(rawRequests.length, rawCountBeforeOffice, "unsupported Office file was downloaded")
  await closePreview()

  assert(
    loadedAssets.some((url) => url.includes("open-file-viewer-renderer")),
    "viewer chunk was not requested on demand"
  )
  assert(
    loadedAssets.some((url) => url.includes("pdf.worker")),
    "PDF worker was not requested"
  )
  assert(rawRequests.length >= 3, "preview raw requests were not observed")
  for (const request of rawRequests) {
    assert.equal(request.headers["x-tenant-id"], undefined)
    assert.equal(request.headers["x-entity-id"], undefined)
  }
  await assertFits()

  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByText(names.text, { exact: true }).click()
  await page.getByRole("dialog").getByText(textBody, { exact: false }).waitFor()
  await assertFits()
  await page.screenshot({
    path: fileURLToPath(new URL("mobile-text.png", screenshots)),
    fullPage: true,
  })
  await closePreview()

  const rootsResponse = await context.request.get(`${webURL}/api/files?limit=100`)
  assert(rootsResponse.ok(), "account root cleanup listing failed")
  const roots = (await rootsResponse.json()).data.items
  const rootID = roots.find((root) => root.scope === "account")?.id
  assert(rootID, "account root was not available for cleanup")
  const itemsResponse = await context.request.get(
    `${webURL}/api/files/${rootID}/items?limit=100`
  )
  assert(itemsResponse.ok(), "account item cleanup listing failed")
  const items = (await itemsResponse.json()).data.items
  for (const item of items.filter(({ name }) => name.startsWith("preview-"))) {
    const deleted = await context.request.delete(
      `${webURL}/api/files/${item.id}?revision=${item.revision}`,
      { headers: { Origin: webURL } }
    )
    assert(deleted.ok(), `preview cleanup failed with ${deleted.status()}`)
  }

  assert.deepEqual(consoleErrors, [], `browser errors: ${consoleErrors.join("\n")}`)
  assert.deepEqual(failedResponses, [], `failed responses: ${failedResponses.join("\n")}`)
  console.log(
    `PASS file preview: icons in 3 views, text/image/PDF, Office fallback, lazy chunks, account auth context, desktop/mobile layout`
  )
} finally {
  await browser.close()
}
