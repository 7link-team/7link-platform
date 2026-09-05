import { isRouteErrorResponse, useRouteError } from "react-router"
import { Button } from "@workspace/ui/components/button"

export function RouteError() {
  const error = useRouteError()

  return (
    <div className="grid min-h-svh place-items-center bg-background p-6">
      <div className="w-full max-w-md space-y-4 text-center">
        <h1 className="text-lg font-medium">页面出了点问题</h1>
        <p className="text-sm text-muted-foreground">
          {routeErrorMessage(error)}
        </p>
        <div className="flex justify-center gap-2">
          <Button variant="outline" onClick={() => window.location.reload()}>
            重新加载
          </Button>
          <Button onClick={() => window.location.assign("/")}>返回首页</Button>
        </div>
      </div>
    </div>
  )
}

function routeErrorMessage(error: unknown): string {
  if (isRouteErrorResponse(error)) {
    return error.status === 404
      ? "找不到这个页面。"
      : `请求未能完成（${error.status}）。`
  }
  // A chunk that fails to load leaves the app on a stale build, so reloading is
  // the fix rather than a retry of the same request.
  if (
    error instanceof Error &&
    /dynamically imported module|Failed to fetch/i.test(error.message)
  ) {
    return "页面资源加载失败，可能是版本已更新，重新加载即可。"
  }
  return "页面渲染时发生了未预期的错误，可以重新加载或返回首页。"
}
