import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItem, type WorkItemQuery } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { boardAction, moveBoardWork } from "./board-actions"

function BoardColumn({ project, state, states, query, revision, limit, onOpen, onMove }: {
  project: string; state: string; states: readonly string[]; query: WorkItemQuery; revision: number; limit: number
  onOpen: (row: WorkItem) => void; onMove: (row: WorkItem, target: string) => void
}) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const key = `board_${state}`
  const raw = Number(params.get(key) ?? 1)
  const page = Number.isSafeInteger(raw) && raw > 0 ? raw : 1
  const [result, setResult] = useState<{ items: WorkItem[]; total: number } | null>(null)
  const [error, setError] = useState("")
  const changePage = (value: number) => setParams(previous => { const next = new URLSearchParams(previous); next.set(key, String(value)); return next })
  useEffect(() => {
    const controller = new AbortController()
    queueMicrotask(() => { if (!controller.signal.aborted) { setResult(null); setError("") } })
    iamApi.workItems(project, { ...query, state: `eq:${state}`, limit, offset: (page - 1) * limit }, controller.signal)
      .then(data => {
        if (controller.signal.aborted) return
        setResult(data)
      })
      .catch(e => { if (!controller.signal.aborted) setError(e instanceof Error ? e.message : t("workUnavailable")) })
    return () => controller.abort()
  }, [project, query, state, page, limit, revision, t])
  useEffect(() => {
    if (result && page > 1 && (page - 1) * limit >= result.total) setParams(previous => { const next = new URLSearchParams(previous); next.set(key, String(Math.max(1, Math.ceil(result.total / limit)))); return next }, { replace: true })
  }, [result, page, limit, key, setParams])
  return <section className="min-w-0 space-y-3 rounded-md border p-3" onDragOver={e => { if (e.dataTransfer.types.includes("application/x-work-item")) e.preventDefault() }} onDrop={e => {
    e.preventDefault()
    try { const row = JSON.parse(e.dataTransfer.getData("application/x-work-item")) as WorkItem; if (row.project_id === project && row.state !== state) onMove(row, state) } catch { /* unrelated drag */ }
  }}>
    <h2 className="font-medium">{t(`workState_${state}`)}: {result?.total ?? "…"}</h2>
    {error && <Alert>{error}</Alert>}
    {!result && !error && <p role="status">{t("loading")}</p>}
    <ul className="grid gap-2">{result?.items.map(row => <li key={row.id} draggable onDragStart={e => { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("application/x-work-item", JSON.stringify(row)) }} className="space-y-2 rounded-md border p-2">
      <Button variant="ghost" className="h-auto min-h-11 w-full justify-start whitespace-normal text-left" onClick={() => onOpen(row)}>#{row.number} {row.title}</Button>
      <label className="block text-sm"><span>{t("workBoardMove")}</span><select className="mt-1 block min-h-11 w-full rounded-md border bg-background p-2" value="" onChange={e => { if (e.target.value) onMove(row, e.target.value) }}><option value="">{t("workBoardChooseColumn")}</option>{states.filter(s => s !== row.state).map(s => <option key={s} value={s}>{t(`workState_${s}`)}</option>)}</select></label>
    </li>)}</ul>
    {result?.items.length === 0 && <p className="text-sm">{t("workEmpty")}</p>}
    <PaginationPages page={page} totalPages={Math.max(1, Math.ceil((result?.total ?? 0) / limit))} disabled={!result} onPageChange={changePage} />
  </section>
}

export function WorkBoard({ project, states, query, revision, onOpen, onChanged }: {
  project: string; states: readonly string[]; query: WorkItemQuery; revision: number; onOpen: (row: WorkItem) => void; onChanged: () => void
}) {
  const t = useTranslations("iam"), allowed = useAllowed()
  const [params, setParams] = useSearchParams()
  const limit = [10, 20, 50].includes(Number(params.get("board_limit"))) ? Number(params.get("board_limit")) : 10
  const [move, setMove] = useState<{ row: WorkItem; target: string } | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const request = (row: WorkItem, target: string) => {
    setError("")
    if (boardAction(row, target, allowed) === "blocked") { setError(t("workBoardGuarded")); return }
    setMove({ row, target })
  }
  return <div className="min-h-0 flex-1 space-y-4 overflow-auto" aria-label={t("workView_board")}>
    <p className="text-sm text-muted-foreground">{t("workBoardColumnHint")}</p>
    <label className="flex items-center gap-2 text-sm">{t("projectPageSize")}<select className="min-h-11 rounded-md border bg-background p-2" value={limit} onChange={e => setParams(previous => { const next = new URLSearchParams(previous); next.set("board_limit", e.target.value); states.forEach(s => next.delete(`board_${s}`)); return next })}>{[10, 20, 50].map(v => <option key={v} value={v}>{v}</option>)}</select></label>
    {error && <Alert>{error}</Alert>}
    <div className="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">{states.map(state => <BoardColumn key={state} project={project} state={state} states={states} query={query} revision={revision} limit={limit} onOpen={onOpen} onMove={request} />)}</div>
    {move && <Dialog open onOpenChange={open => { if (!open && !busy) setMove(null) }}><DialogContent><DialogHeader><DialogTitle>{t("workBoardMove")}</DialogTitle><DialogDescription>{t("workBoardConfirmHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<p>{move.row.title}: {t(`workState_${move.row.state}`)} → {t(`workState_${move.target}`)}</p><Button disabled={busy} onClick={async () => {
      if (pending.current) return
      pending.current = true; setBusy(true)
      const identity = `${project}:${move.row.id}:${move.row.version}:${move.target}`
      if (attempt.current?.identity !== identity) attempt.current = { identity, key: crypto.randomUUID() }
      try {
        const current = await moveBoardWork(iamApi, project, move.row, move.target, allowed, attempt.current.key)
        attempt.current = null; setMove(null)
        if (current.state !== move.target) setError(t("workBoardUnchanged"))
        onChanged()
      } catch (e) { const message = e instanceof Error ? e.message : "workUnavailable"; setError(message.startsWith("workBoard") ? t(message) : message) }
      finally { pending.current = false; setBusy(false) }
    }}>{busy ? t("loading") : t("confirm")}</Button></DialogContent></Dialog>}
  </div>
}
