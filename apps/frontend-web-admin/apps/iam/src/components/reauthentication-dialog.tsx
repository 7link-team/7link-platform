import { useEffect, useState, type FormEvent } from "react"
import { Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Alert } from "./alert"
import {
  iamApi,
  type IdentityLink,
  type ReauthenticationMethod,
} from "../lib/iam-api"
import { getPasskeyCredential } from "../lib/webauthn"

const continuationKey = "iam.reauthentication"

export function ReauthenticationDialog({
  purpose,
  target,
  onVerified,
  onClose,
}: {
  purpose: string
  target: string
  onVerified: (proof: string) => Promise<void>
  onClose: () => void
}) {
  const t = useTranslations("iam")
  const [available, setAvailable] = useState<{
    methods: ReauthenticationMethod[]
    mfa_required: boolean
    links: IdentityLink[]
  } | null>(null)
  const [method, setMethod] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [reload, setReload] = useState(0)
  const [canRecover, setCanRecover] = useState(false)
  useEffect(() => {
    let active = true
    void iamApi.capabilities().then(
      (capabilities) => {
        if (active) setCanRecover(capabilities.password_recovery)
      },
      () => {
        if (active) setCanRecover(false)
      }
    )
    void Promise.all([
      iamApi.reauthenticationMethods(),
      iamApi.identityLinks(),
      iamApi.identityLinkProviders(),
    ]).then(
      ([methods, links, providers]) => {
        if (!active) return
        const usable = links.filter((link) =>
          providers.some((provider) => provider.id === link.provider_id)
        )
        setAvailable({ ...methods, links: usable })
        setMethod(
          methods.methods[0] ?? (usable[0] ? `federation:${usable[0].id}` : "")
        )
        setError("")
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [reload])
  useEffect(() => {
    const raw = sessionStorage.getItem(continuationKey)
    if (!raw) return
    let continuation: { purpose: string; target: string; proof: string }
    try {
      continuation = JSON.parse(raw)
    } catch {
      sessionStorage.removeItem(continuationKey)
      return
    }
    if (continuation.purpose !== purpose || continuation.target !== target)
      return
    sessionStorage.removeItem(continuationKey)
    void Promise.resolve().then(async () => {
      setBusy(true)
      try {
        await onVerified(continuation.proof)
      } catch (cause) {
        setError((cause as Error).message)
      } finally {
        setBusy(false)
      }
    })
  }, [purpose, target, onVerified])
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    setBusy(true)
    setError("")
    try {
      const federation = method.startsWith("federation:")
      const challenge = await iamApi.createReauthenticationChallenge({
        purpose,
        target,
        method: federation ? "federation" : (method as ReauthenticationMethod),
      })
      if (federation) {
        const link = available?.links.find(
          (item) => `federation:${item.id}` === method
        )
        if (!link) throw new Error("reauthenticationUnavailable")
        const result = await iamApi.createIdentityLinkTransaction({
          provider_id: link.provider_id,
          existing_link_id: link.id,
          proof: challenge.proof,
          return_url: location.origin + location.pathname,
        })
        sessionStorage.setItem(
          continuationKey,
          JSON.stringify({ purpose, target, proof: challenge.proof })
        )
        location.assign(result.authorization_url)
        return
      }
      const credentials =
        method === "passkey"
          ? {
              response: await getPasskeyCredential(
                challenge.options ?? { publicKey: {} }
              ),
            }
          : {
              password: String(data.get("password") ?? ""),
              ...(available?.mfa_required
                ? { code: String(data.get("code") ?? "") }
                : {}),
            }
      const verification = await iamApi.verifyReauthentication({
        proof: challenge.proof,
        ...credentials,
      })
      if (!verification.verified) throw new Error("reauthenticationFailed")
      await onVerified(challenge.proof)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          sessionStorage.removeItem(continuationKey)
          onClose()
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("confirmIdentity")}</DialogTitle>
          <DialogDescription>
            {t("confirmIdentityDescription")}
          </DialogDescription>
        </DialogHeader>
        {error && <Alert>{t.has(error) ? t(error) : error}</Alert>}
        {canRecover && (error || (available && !method)) && (
          <Button asChild variant="outline">
            <Link to="/recover" onClick={onClose}>
              {t("forgotPassword")}
            </Link>
          </Button>
        )}
        {!available ? (
          error ? (
            <Button onClick={() => setReload((value) => value + 1)}>
              {t("retryNow")}
            </Button>
          ) : (
            <p role="status">{t("loading")}</p>
          )
        ) : !method ? (
          <p>{t("reauthenticationUnavailable")}</p>
        ) : (
          <form onSubmit={submit} className="grid gap-4">
            <Label htmlFor="reauth-method">{t("verificationMethod")}</Label>
            <select
              id="reauth-method"
              value={method}
              disabled={busy}
              onChange={(event) => setMethod(event.target.value)}
              className="min-h-11 rounded-md border bg-background px-3"
            >
              {available.methods.map((value) => (
                <option key={value} value={value}>
                  {t(value === "password" ? "password" : "passkey")}
                </option>
              ))}
              {available.links.map((link) => (
                <option key={link.id} value={`federation:${link.id}`}>
                  {link.provider_name}
                </option>
              ))}
            </select>
            {method === "password" && (
              <>
                <Label htmlFor="reauth-password">{t("currentPassword")}</Label>
                <Input
                  id="reauth-password"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  required
                  disabled={busy}
                />
                {available.mfa_required && (
                  <>
                    <Label htmlFor="reauth-code">{t("verificationCode")}</Label>
                    <Input
                      id="reauth-code"
                      name="code"
                      autoComplete="one-time-code"
                      required
                      disabled={busy}
                    />
                  </>
                )}
              </>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={onClose}
              >
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? t("loading") : t("continue")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
