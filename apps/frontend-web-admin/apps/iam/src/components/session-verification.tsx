import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { useAuth } from "./auth"
import { Alert } from "./alert"

export function SessionVerification() {
  const t = useTranslations("iam")
  const { status, refresh } = useAuth()
  return (
    <div className="grid min-h-svh place-items-center bg-background p-4">
      {status === "error" ? (
        <div className="w-full max-w-md">
          <Alert>
            <p>{t("sessionVerificationFailed")}</p>
            <Button
              className="mt-3 min-h-11"
              variant="outline"
              onClick={() => void refresh().catch(() => undefined)}
            >
              {t("retryNow")}
            </Button>
          </Alert>
        </div>
      ) : (
        <p role="status">{t("verifyingSession")}</p>
      )}
    </div>
  )
}
