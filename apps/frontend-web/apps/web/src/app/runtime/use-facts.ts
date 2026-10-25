import { useEffect, useState } from "react"

/** Clear old-scope facts immediately in the render result and abort superseded requests. */
export function useFacts<T>(scope: string, load: (signal: AbortSignal) => Promise<T>) {
  const [revision, setRevision] = useState(0)
  const [state, setState] = useState<{scope: string; revision: number; data?: T; error?: string}>()
  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal).then(data => {
      if (!controller.signal.aborted) setState({scope, revision, data})
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setState({scope, revision, error: cause instanceof Error ? cause.message : String(cause)})
    })
    return () => controller.abort()
  }, [load, scope, revision])
  const current = state?.scope === scope && state.revision === revision ? state : undefined
  return {data: current?.data, error: current?.error, loading: !current, refresh: () => setRevision(value => value + 1)}
}
