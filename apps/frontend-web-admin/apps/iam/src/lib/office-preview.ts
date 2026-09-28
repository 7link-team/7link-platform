export type OfficePreviewFit =
  | "contain"
  | "cover"
  | "width"
  | "height"
  | "actual"
  | "scale-down"

export function officePreviewFit(
  fileName: string,
  mimeType: string
): OfficePreviewFit {
  const extension = fileName.split(".").pop()?.toLowerCase() ?? ""
  const mime = mimeType.toLowerCase()
  if (extension === "docx" || mime.includes("wordprocessingml")) return "width"
  if (extension === "xlsx" || mime.includes("spreadsheetml")) return "actual"
  return "contain"
}

export function presentationPageLabel(
  page: number,
  title: string,
  locale: "zh-CN" | "en-US"
) {
  const prefix = locale === "zh-CN" ? `第 ${page} 页` : `Slide ${page}`
  return title ? `${prefix}: ${title}` : prefix
}
