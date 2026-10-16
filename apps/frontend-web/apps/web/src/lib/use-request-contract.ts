import { useEffect, useState } from "react"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import type { OperationRequestContract } from "@workspace/ui/components/auto-form/contract"
import type { ApiRoute } from "./api-route"
import { iamApi } from "./iam-api"
import { getAccountViewRevision } from "./account-view"

/** Loads the selected operation's rules. A changed operation/language never uses stale metadata. */
export function useRequestContract(route: ApiRoute | null) {
  const locale = useLocale()
  const [attempt, setAttempt] = useState(0)
  const revision = getAccountViewRevision()
  const key = `${route?.method}:${route?.url}:${locale}:${revision}:${attempt}`
  const [result, setResult] = useState<{
    key: string
    contract?: OperationRequestContract
    failed: boolean
  }>()
  useEffect(() => {
    if (!route) return
    const controller = new AbortController()
    void iamApi
      .requestContract(route, { signal: controller.signal, locale })
      .then(
        (contract) => {
          if (!controller.signal.aborted)
            setResult({ key, contract, failed: false })
        },
        () => {
          if (!controller.signal.aborted) setResult({ key, failed: true })
        }
      )
    return () => controller.abort()
  }, [route, locale, key])
  const current = result?.key === key ? result : undefined
  return {
    contract: current?.contract ?? null,
    failed: current?.failed ?? false,
    retry: () => setAttempt((value) => value + 1),
  }
}
