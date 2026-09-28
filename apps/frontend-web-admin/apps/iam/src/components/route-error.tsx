import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { isRouteErrorResponse, useRouteError } from "react-router"
import { Button } from "@workspace/ui/components/button"

export function RouteError() {
  const t = useTranslations("iam")

  const error = useRouteError()

  return (
    <div className="grid min-h-svh place-items-center bg-background p-6">
      <div className="w-full max-w-md space-y-4 text-center">
        <h1 className="text-lg font-medium">{t("pageError")}</h1>
        <p className="text-sm text-muted-foreground">
          {t(routeErrorMessage(error), {
            status: isRouteErrorResponse(error) ? error.status : 0,
          })}
        </p>
        <div className="flex justify-center gap-2">
          <Button variant="outline" onClick={() => window.location.reload()}>
            {t("reload")}
          </Button>
          <Button onClick={() => window.location.assign("/")}>
            {t("backHome")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function routeErrorMessage(error: unknown): string {
  if (isRouteErrorResponse(error)) {
    return error.status === 404 ? "pageNotFound" : "requestFailed"
  }
  // A chunk that fails to load leaves the app on a stale build, so reloading is
  // the fix rather than a retry of the same request.
  if (
    error instanceof Error &&
    /dynamically imported module|Failed to fetch/i.test(error.message)
  ) {
    return "pageLoadError"
  }
  return "pageRenderError"
}
