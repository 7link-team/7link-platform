import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
} from "react"
import {
  ArrowUpFromLine,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  UserMinus,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
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
import { SimpleSelect } from "@workspace/ui/components/select"
import { Switch } from "@workspace/ui/components/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  ApiError,
  createIamApi,
  type SCIMTarget,
  type SCIMPushResult,
  type SCIMResourceInput,
  type ServiceAccount,
} from "../../lib/iam-api"

interface TargetDraft {
  name: string
  url: string
  token: string
  account: string
  enabled: boolean
}
const emptyDraft: TargetDraft = {
  name: "",
  url: "",
  token: "",
  account: "",
  enabled: true,
}

export function SCIMTargets({ tenant }: { tenant: string }) {
  const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const [targets, setTargets] = useState<SCIMTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [available, setAvailable] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [editing, setEditing] = useState<SCIMTarget | null | undefined>()
  const [draft, setDraft] = useState<TargetDraft>({ ...emptyDraft })
  const [conflict, setConflict] = useState(false)
  const [deleting, setDeleting] = useState<SCIMTarget | null>(null)
  const [operation, setOperation] = useState<{
    target: SCIMTarget
    remove: boolean
  } | null>(null)
  const [resource, setResource] = useState<SCIMResourceInput>({
    resource_type: "User",
    resource_id: "",
  })
  const [result, setResult] = useState<SCIMPushResult | null>(null)
  const [accounts, setAccounts] = useState<ServiceAccount[]>([])
  const [accountError, setAccountError] = useState("")
  const [choices, setChoices] = useState<{ id: string; name: string }[]>([])
  const [choiceError, setChoiceError] = useState("")

  const refresh = useCallback(async () => {
    try {
      setTargets(await api.scimTargets())
      setAvailable(true)
    } catch (cause) {
      setTargets([])
      setAvailable(false)
      throw cause
    }
  }, [api])
  useEffect(() => {
    let active = true
    void api
      .scimTargets()
      .then(
        (items) => {
          if (active) {
            setTargets(items)
            setAvailable(true)
          }
        },
        (cause: Error) => active && setError(cause.message)
      )
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
  }, [api])
  useEffect(() => {
    if (editing === undefined) return
    let active = true
    void api.serviceAccounts(draft.account).then(
      (data) => {
        if (active) {
          setAccounts(data.items)
          setAccountError("")
        }
      },
      (cause: Error) => {
        if (active) {
          setAccounts([])
          setAccountError(cause.message)
        }
      }
    )
    return () => {
      active = false
    }
  }, [api, editing, draft.account])
  useEffect(() => {
    if (!operation) return
    let active = true
    const load =
      resource.resource_type === "User"
        ? api.principals().then((data) =>
            data.items.map((item) => ({
              id: item.id,
              name: item.display_name || item.login_name || item.id,
            }))
          )
        : api
            .groups()
            .then((data) =>
              data.map((item) => ({ id: item.id, name: item.name }))
            )
    void load.then(
      (items) => active && setChoices(items),
      (cause: Error) => active && setChoiceError(cause.message)
    )
    return () => {
      active = false
    }
  }, [api, operation, resource.resource_type])

  const edit = (target: SCIMTarget | null) => {
    setError("")
    setAccountError("")
    setAccounts([])
    setConflict(false)
    setDraft(
      target
        ? {
            name: target.name,
            url: target.base_url,
            token: "",
            account: target.service_account_id ?? "",
            enabled: target.status === "active",
          }
        : { ...emptyDraft }
    )
    setEditing(target)
  }
  const closeEditor = () => {
    setEditing(undefined)
    setDraft({ ...emptyDraft })
    setAccounts([])
    setConflict(false)
  }
  const reload = async () => {
    if (!editing) return
    setBusy(true)
    setError("")
    try {
      const items = await api.scimTargets()
      setTargets(items)
      const current = items.find((item) => item.id === editing.id)
      if (current) edit(current)
      else closeEditor()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (conflict) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const body = {
        name: draft.name.trim(),
        base_url: draft.url.trim(),
        service_account_id: draft.account.trim() || null,
      }
      if (editing) {
        await api.updateSCIMTarget(editing.id, {
          ...body,
          status: draft.enabled ? "active" : "disabled",
          version: editing.version,
          ...(draft.token ? { bearer_token: draft.token } : {}),
        })
      } else {
        await api.createSCIMTarget({ ...body, bearer_token: draft.token })
      }
      setNotice(editing ? "出站目标已更新" : "出站目标已创建")
      closeEditor()
      await refresh()
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409 && editing)
        setConflict(true)
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const removeTarget = async () => {
    if (!deleting) return
    setBusy(true)
    setError("")
    try {
      await api.deleteSCIMTarget(deleting.id)
      setDeleting(null)
      setNotice("出站配置已删除")
      setResult(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const openOperation = (target: SCIMTarget, remove: boolean) => {
    setError("")
    setChoices([])
    setChoiceError("")
    setResult(null)
    setResource({ resource_type: "User", resource_id: "" })
    setOperation({ target, remove })
  }
  const runOperation = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!operation) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const body = { ...resource, resource_id: resource.resource_id.trim() }
      if (operation.remove) {
        await api.deprovisionSCIMResource(operation.target.id, body)
        setNotice("远端资源已移除")
      } else {
        setResult(await api.pushSCIMResource(operation.target.id, body))
        setNotice("资源已推送")
      }
      setOperation(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <PageHeader
        title="SCIM 出站目标"
        action={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon"
              title="刷新目标"
              aria-label="刷新目标"
              disabled={busy || loading}
              onClick={() => {
                setError("")
                setLoading(true)
                void refresh()
                  .catch((cause: Error) => setError(cause.message))
                  .finally(() => setLoading(false))
              }}
            >
              <RefreshCw className={loading ? "animate-spin" : ""} />
            </Button>
            <Button disabled={!available || busy} onClick={() => edit(null)}>
              <Plus />
              创建目标
            </Button>
          </div>
        }
      />
      {error && editing === undefined && !operation && !deleting && (
        <Alert>{error}</Alert>
      )}
      {notice && (
        <div role="status" className="mb-4 text-sm">
          {notice}
          {result && (
            <span className="ml-2 font-mono text-xs break-all">
              远端 ID: {result.remote_id || result.external_id}
            </span>
          )}
        </div>
      )}
      <div className="overflow-hidden rounded-md border bg-card">
        <Table className="min-w-[950px]">
          <TableHeader>
            <TableRow>
              <TableHead>目标</TableHead>
              <TableHead>服务账号</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>版本</TableHead>
              <TableHead className="w-44 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {targets.map((target) => (
              <TableRow key={target.id} data-target-id={target.id}>
                <TableCell>
                  <strong className="block text-sm">{target.name}</strong>
                  <span className="block max-w-lg font-mono text-xs break-all text-muted-foreground">
                    {target.base_url}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {target.service_account_id ?? "未绑定"}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      target.status === "active" ? "secondary" : "outline"
                    }
                  >
                    {target.status === "active" ? "启用" : "停用"}
                  </Badge>
                </TableCell>
                <TableCell>{target.version}</TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      disabled={busy}
                      title="编辑目标"
                      aria-label={`编辑 ${target.name}`}
                      onClick={() => edit(target)}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      disabled={
                        busy ||
                        target.status !== "active" ||
                        !target.service_account_id
                      }
                      title="推送资源"
                      aria-label={`推送到 ${target.name}`}
                      onClick={() => openOperation(target, false)}
                    >
                      <ArrowUpFromLine />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      disabled={
                        busy ||
                        target.status !== "active" ||
                        !target.service_account_id
                      }
                      title="移除远端资源"
                      aria-label={`从 ${target.name} 移除资源`}
                      onClick={() => openOperation(target, true)}
                    >
                      <UserMinus />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      disabled={busy}
                      title="删除出站配置"
                      aria-label={`删除 ${target.name}`}
                      onClick={() => {
                        setError("")
                        setDeleting(target)
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {!targets.length && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            {loading ? "加载中" : available ? "暂无出站目标" : "出站目标不可用"}
          </div>
        )}
      </div>

      <Dialog
        open={editing !== undefined}
        onOpenChange={(open) => {
          if (!open && !busy) closeEditor()
        }}
      >
        <DialogContent className="grid max-h-[calc(100svh-2rem)] gap-5 overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editing ? "编辑出站目标" : "创建出站目标"}
            </DialogTitle>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <form id="scim-target-form" className="grid gap-4" onSubmit={save}>
            <div className="grid gap-2">
              <Label htmlFor="scim-target-name">名称</Label>
              <Input
                id="scim-target-name"
                value={draft.name}
                onChange={(event) =>
                  setDraft({ ...draft, name: event.target.value })
                }
                required
                maxLength={128}
                disabled={busy || conflict}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="scim-target-url">SCIM Base URL</Label>
              <Input
                id="scim-target-url"
                type="url"
                value={draft.url}
                onChange={(event) =>
                  setDraft({ ...draft, url: event.target.value })
                }
                required
                maxLength={1024}
                disabled={busy || conflict}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="scim-target-token">
                {editing ? "新 Bearer Token（可选）" : "Bearer Token"}
              </Label>
              <Input
                id="scim-target-token"
                type="password"
                value={draft.token}
                onChange={(event) =>
                  setDraft({ ...draft, token: event.target.value })
                }
                required={!editing}
                maxLength={4096}
                autoComplete="new-password"
                disabled={busy || conflict}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="scim-target-account">服务账号</Label>
              <Input
                id="scim-target-account"
                list="scim-target-accounts"
                value={draft.account}
                onChange={(event) =>
                  setDraft({ ...draft, account: event.target.value })
                }
                pattern="[1-9][0-9]*"
                maxLength={64}
                autoComplete="off"
                placeholder="未绑定"
                disabled={busy || conflict}
              />
              <datalist id="scim-target-accounts">
                {accounts.map((account) => (
                  <option key={account.id} value={account.id}>
                    {account.display_name || account.login_name}
                  </option>
                ))}
              </datalist>
              {accountError && <Alert>{accountError}</Alert>}
            </div>
            {editing && (
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="scim-target-enabled">启用目标</Label>
                <Switch
                  id="scim-target-enabled"
                  checked={draft.enabled}
                  onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
                  disabled={busy || conflict}
                />
              </div>
            )}
          </form>
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={busy} onClick={closeEditor}>
              取消
            </Button>
            {conflict ? (
              <Button disabled={busy} onClick={() => void reload()}>
                <RefreshCw />
                重新加载
              </Button>
            ) : (
              <Button type="submit" form="scim-target-form" disabled={busy}>
                {busy ? "保存中" : "保存"}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(operation)}
        onOpenChange={(open) => {
          if (!open && !busy) setOperation(null)
        }}
      >
        <DialogContent className="grid max-h-[calc(100svh-2rem)] gap-5 overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {operation?.remove ? "移除远端资源" : "推送资源"}
            </DialogTitle>
            <DialogDescription className="break-all">
              {operation?.target.name} · {operation?.target.base_url}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <form
            id="scim-resource-form"
            className="grid gap-4"
            onSubmit={runOperation}
          >
            <div className="grid gap-2">
              <Label htmlFor="scim-resource-type">资源类型</Label>
              <SimpleSelect
                id="scim-resource-type"
                value={resource.resource_type}
                onValueChange={(value) => {
                  setChoices([])
                  setChoiceError("")
                  setResource({
                    resource_type: value as SCIMResourceInput["resource_type"],
                    resource_id: "",
                  })
                }}
                options={[
                  { value: "User", label: "用户" },
                  { value: "Group", label: "用户组" },
                ]}
                disabled={busy}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="scim-resource-id">
                {resource.resource_type === "User" ? "主体 ID" : "用户组 ID"}
              </Label>
              <Input
                id="scim-resource-id"
                list="scim-resource-options"
                value={resource.resource_id}
                onChange={(event) =>
                  setResource({ ...resource, resource_id: event.target.value })
                }
                required
                pattern="[1-9][0-9]*"
                maxLength={64}
                autoComplete="off"
                disabled={busy}
              />
              <datalist id="scim-resource-options">
                {choices.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </datalist>
              {choiceError && <Alert>{choiceError}</Alert>}
            </div>
          </form>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setOperation(null)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="scim-resource-form"
              disabled={busy}
              variant={operation?.remove ? "destructive" : "default"}
            >
              {operation?.remove ? <UserMinus /> : <ArrowUpFromLine />}
              {busy ? "处理中" : operation?.remove ? "确认移除" : "推送"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(open) => {
          if (!open && !busy) setDeleting(null)
        }}
      >
        <DialogContent className="grid gap-5">
          <DialogHeader>
            <DialogTitle>删除出站配置</DialogTitle>
            <DialogDescription>
              删除 {deleting?.name} 的配置及本地同步映射？远端资源不会被删除。
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDeleting(null)}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void removeTarget()}
            >
              <Trash2 />
              {busy ? "删除中" : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
