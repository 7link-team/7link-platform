import {
  lazy,
  Suspense,
  useEffect,
  useState,
  type ReactNode,
} from "react"
import { Download, FileWarning, LoaderCircle } from "lucide-react"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  FILE_PREVIEW_MAX_BYTES,
  classifyFilePresentation,
  fileSizeLabel,
  type FileBrowserNode,
} from "../lib/file-browser"
import { FileTypeIcon } from "./file-type-icon"
import { ApiError } from "../lib/iam-api"

const OpenFileViewerRenderer = lazy(() => import("./open-file-viewer-renderer"))

type PreviewState =
  | { status: "loading" }
  | { status: "ready"; blob: Blob }
  | { status: "error"; tooLarge: boolean }

export function FilePreviewDialog({
  node,
  loadFile,
  onDownload,
  onClose,
}: {
  node: FileBrowserNode
  loadFile: (
    node: FileBrowserNode,
    maxBytes: number,
    signal: AbortSignal
  ) => Promise<Blob>
  onDownload?: () => void | Promise<unknown>
  onClose: () => void
}) {
  const tf = useTranslations("files")
  const locale = useLocale()
  const presentation = classifyFilePresentation(node)
  const unsupported = presentation.preview === "unsupported"
  const knownOversize =
    node.size !== undefined && node.size > FILE_PREVIEW_MAX_BYTES
  const [state, setState] = useState<PreviewState>({ status: "loading" })

  useEffect(() => {
    if (unsupported || knownOversize) return
    const controller = new AbortController()
    let active = true
    void loadFile(node, FILE_PREVIEW_MAX_BYTES, controller.signal)
      .then((blob) => {
        if (active) setState({ status: "ready", blob })
      })
      .catch((cause: unknown) => {
        if (!active || controller.signal.aborted) return
        setState({
          status: "error",
          tooLarge:
            cause instanceof ApiError && cause.code === "file_preview_too_large",
        })
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [knownOversize, loadFile, node, unsupported])

  const unavailableMessage = unsupported
    ? tf("previewUnsupported")
    : knownOversize || (state.status === "error" && state.tooLarge)
      ? tf("previewTooLarge", {
          limit: fileSizeLabel(FILE_PREVIEW_MAX_BYTES, locale),
        })
      : tf("previewLoadFailed")

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[calc(100dvh-1rem)] max-h-[960px] w-[calc(100%-1rem)] max-w-[96rem] flex-col gap-0 overflow-hidden p-0 sm:w-[calc(100%-2rem)]">
        <DialogHeader className="shrink-0 border-b px-4 py-3 pe-14 text-start">
          <div className="flex min-w-0 items-center gap-3">
            <FileTypeIcon node={node} className="size-7" showBadge />
            <div className="min-w-0">
              <DialogTitle className="truncate text-base" title={node.name}>
                {node.name}
              </DialogTitle>
              <DialogDescription className="mt-1">
                {fileSizeLabel(node.size, locale)}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="min-h-0 flex-1 bg-muted/20">
          {!unsupported && !knownOversize && state.status === "ready" ? (
            <Suspense
              fallback={
                <PreviewStatus>
                  <LoaderCircle className="size-8 animate-spin" />
                  <span>{tf("previewLoading")}</span>
                </PreviewStatus>
              }
            >
              <OpenFileViewerRenderer
                blob={state.blob}
                name={node.name}
                mimeType={node.detectedType || state.blob.type}
                locale={locale === "zh-CN" ? "zh-CN" : "en-US"}
                presentationNavigationLabel={tf("presentationPages")}
              />
            </Suspense>
          ) : !unsupported && !knownOversize && state.status === "loading" ? (
            <PreviewStatus>
              <LoaderCircle className="size-8 animate-spin" />
              <span>{tf("previewLoading")}</span>
            </PreviewStatus>
          ) : (
            <PreviewStatus>
              <FileWarning className="size-9" />
              <span>{unavailableMessage}</span>
              {onDownload && (
                <Button type="button" onClick={() => void onDownload()}>
                  <Download />
                  {tf("download")}
                </Button>
              )}
            </PreviewStatus>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function PreviewStatus({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full min-h-64 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted-foreground">
      {children}
    </div>
  )
}
