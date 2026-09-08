"use client"

import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { useCallback, useEffect, useState } from "react"

import { Alert } from "../../components/alert"
import type {
  IamApi,
  Principal,
  PrincipalLifecycleBlocker,
  PrincipalLifecycleStatus,
} from "../../lib/iam-api"

// 每条阻塞原因都对应运维要去做的一件具体的事，所以写成祈使句而不是状态描述。
const BLOCKER_TEXT: Record<PrincipalLifecycleBlocker, string> = {
  already_anonymized: "该主体已被擦除，记录已是墓碑状态，无法再更改。",
  legal_hold: "存在法律保留。先解除保留，才能执行擦除。",
  no_retention_recorded: "尚未记录保留期。先设定保留期限，擦除才有依据。",
  retention_not_expired: "保留期尚未到期。到期后才允许擦除。",
  not_suspended: "主体仍可登录。先停用它，再执行擦除。",
}

function toDateInput(value: string | null): string {
  if (!value) return ""
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ""
  return parsed.toISOString().slice(0, 10)
}

export function PrincipalLifecycleDialog({
  principal,
  iamApi,
  onClose,
}: {
  principal: Principal | null
  iamApi: IamApi
  onClose: () => void
}) {
  const [status, setStatus] = useState<PrincipalLifecycleStatus | null>(null)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [busy, setBusy] = useState(false)
  const [retainUntil, setRetainUntil] = useState("")
  const [legalHold, setLegalHold] = useState(false)
  const [reference, setReference] = useState("")
  const [confirmation, setConfirmation] = useState("")

  const load = useCallback(
    (id: string) =>
      iamApi.principalLifecycle(id).then((next) => {
        setError("")
        setStatus(next)
        setRetainUntil(toDateInput(next.lifecycle.retain_until))
        setLegalHold(next.lifecycle.legal_hold)
        setReference(next.lifecycle.reference)
      }),
    [iamApi]
  )

  useEffect(() => {
    if (!principal) return
    void load(principal.id).catch((cause: Error) => setError(cause.message))
  }, [principal, load])

  const saveRetention = async () => {
    if (!principal || !status) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      // 版本随每次读取带回：两个管理员从同一屏做决定时，后一个会被告知策略已变，
      // 而不是悄悄覆盖前一个。
      await iamApi.setPrincipalRetention(principal.id, {
        version: status.lifecycle.version,
        retain_until: new Date(`${retainUntil}T00:00:00Z`).toISOString(),
        legal_hold: legalHold,
        reference,
      })
      setNotice("保留期已更新")
      await load(principal.id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const anonymize = async () => {
    if (!principal || !status) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await iamApi.anonymizePrincipal(principal.id, {
        version: status.lifecycle.version,
        confirmation,
      })
      setNotice("该主体的个人数据已被永久擦除")
      setConfirmation("")
      await load(principal.id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const blocked = (status?.blockers.length ?? 0) > 0
  const namedCorrectly =
    status !== null && confirmation.trim() === status.login_name

  return (
    <Dialog
      open={Boolean(principal)}
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="grid max-h-[85vh] gap-5 overflow-y-auto">
        <DialogHeader>
          <DialogTitle>数据保留与擦除</DialogTitle>
          <DialogDescription>
            这是平台级操作，不属于任何单个租户：擦除会移除该主体在所有模块中的个人数据，
            且不可撤销。
          </DialogDescription>
        </DialogHeader>

        {error && <Alert>{error}</Alert>}
        {notice && (
          <p className="rounded-md border px-3 py-2 text-sm" role="status">
            {notice}
          </p>
        )}

        {status && (
          <>
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt className="text-muted-foreground">登录名</dt>
              <dd>{status.login_name}</dd>
              <dt className="text-muted-foreground">状态</dt>
              <dd>
                {status.anonymized
                  ? "已擦除（墓碑）"
                  : status.suspended
                    ? "已停用"
                    : "启用中"}
              </dd>
              <dt className="text-muted-foreground">策略版本</dt>
              <dd>{status.lifecycle.version}</dd>
            </dl>

            {blocked && (
              <ul className="grid gap-1 text-sm">
                {status.blockers.map((blocker) => (
                  <li key={blocker}>· {BLOCKER_TEXT[blocker]}</li>
                ))}
              </ul>
            )}

            {!status.anonymized && (
              <>
                <div className="grid gap-3">
                  <div className="grid gap-1.5">
                    <Label htmlFor="retain-until">保留至</Label>
                    <Input
                      id="retain-until"
                      type="date"
                      value={retainUntil}
                      disabled={busy}
                      onChange={(event) => setRetainUntil(event.target.value)}
                    />
                  </div>
                  <div className="grid gap-1.5">
                    <Label htmlFor="retention-reference">
                      案件编号（不要填入当事人本人的信息）
                    </Label>
                    <Input
                      id="retention-reference"
                      value={reference}
                      maxLength={200}
                      disabled={busy}
                      onChange={(event) => setReference(event.target.value)}
                    />
                  </div>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={legalHold}
                      disabled={busy}
                      onCheckedChange={(value) => setLegalHold(value === true)}
                    />
                    法律保留（无论期限如何，一律阻止擦除）
                  </label>
                  <Button
                    variant="outline"
                    disabled={busy || !retainUntil || !reference.trim()}
                    onClick={() => void saveRetention()}
                  >
                    {busy ? "处理中" : "保存保留期"}
                  </Button>
                </div>

                <div className="grid gap-1.5 border-t pt-4">
                  <Label htmlFor="anonymize-confirmation">
                    输入登录名 <b>{status.login_name}</b> 以确认永久擦除
                  </Label>
                  <Input
                    id="anonymize-confirmation"
                    value={confirmation}
                    disabled={busy || blocked}
                    autoComplete="off"
                    onChange={(event) => setConfirmation(event.target.value)}
                  />
                </div>
              </>
            )}
          </>
        )}

        <DialogFooter className="gap-2">
          <Button variant="outline" disabled={busy} onClick={onClose}>
            关闭
          </Button>
          {status && !status.anonymized && (
            <Button
              variant="destructive"
              disabled={busy || blocked || !namedCorrectly}
              onClick={() => void anonymize()}
            >
              {busy ? "擦除中" : "永久擦除"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
