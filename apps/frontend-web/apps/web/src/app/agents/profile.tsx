import { useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { profileApi, type AgentProfile, type AvatarCandidate, type ProfilePatch } from "./profile-api"

export function AgentProfileEditor({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [profile, setProfile] = useState<AgentProfile | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [style, setStyle] = useState("PIXEL")
  const [batch, setBatch] = useState(0)
  const [candidates, setCandidates] = useState<AvatarCandidate[]>([])
  const [selected, setSelected] = useState<AvatarCandidate | null>(null)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void profileApi.read(tenant, agent, controller.signal).then(value => { if (!controller.signal.aborted) { setProfile(value); setStyle(value.avatar_style); setError("") } }).catch((cause: Error) => { if (!controller.signal.aborted) setError(cause.message) })
    return () => controller.abort()
  }, [tenant, agent, revision])
  useEffect(() => {
    const controller = new AbortController()
    void profileApi.candidates(tenant, agent, style, batch, controller.signal).then(value => { if (!controller.signal.aborted) setCandidates(value) }).catch((cause: Error) => { if (!controller.signal.aborted) setError(cause.message) })
    return () => controller.abort()
  }, [tenant, agent, style, batch])
  const canEdit = allowed("agent_manage")
  return <section className="space-y-4" aria-label={t("agentProfileTitle")}>
    <h2 className="font-medium">{t("agentProfileTitle")}</h2>
    {error && <Alert>{error}</Alert>}
    <Button type="button" variant="outline" disabled={busy} onClick={() => { setProfile(null); setSelected(null); setRevision(value => value + 1) }}>{t("refresh")}</Button>
    {!profile ? <p role="status">{error ? t("agentBrowseFailed") : t("loading")}</p> : <>
      {profile.avatar_url && <img width={64} height={64} src={profile.avatar_url} alt="" />}
      <p className="text-sm text-muted-foreground">{profile.runtime} · {profile.model} · {profile.handle} · {t("agentProfileRevision")} {profile.config_revision}</p>
      {profile.running_config_revision !== null && profile.running_config_revision !== profile.config_revision && <p role="status">{t("agentProfileRestartRequired")}</p>}
      <fieldset disabled={!canEdit || busy} className="space-y-3"><legend className="text-sm">{t("avatarStyle")}</legend>
        <select aria-label={t("avatarStyle")} className="min-h-11 rounded-md border bg-background px-3" value={style} onChange={event => { setStyle(event.target.value); setSelected(null); setCandidates([]) }}>{["PIXEL", "ROBOT", "MONSTER", "GEOMETRIC"].map(value => <option key={value} value={value}>{t(`avatarStyle_${value.toLowerCase()}`)}</option>)}</select>
        <div className="flex flex-wrap gap-2">{candidates.map(candidate => <button key={candidate.seed} type="button" aria-label={`${t("agentAvatarCandidate")} ${candidate.seed}`} aria-pressed={selected?.seed === candidate.seed} className="rounded-md border p-1 aria-pressed:ring-2 aria-pressed:ring-ring" onClick={() => setSelected(candidate)}><img width={64} height={64} src={candidate.url} alt="" /></button>)}</div>
        <Button type="button" variant="outline" onClick={() => { setBatch(value => value + 1); setSelected(null); setCandidates([]) }}>{t("agentAvatarRefresh")}</Button>
      </fieldset>
      <AutoForm key={`${profile.version}:${revision}`} fields={{
        name: { name: t("agentName"), type: "text", value: profile.name, disabled: !canEdit || busy },
        persona: { name: t("agentPersona"), type: "textarea", value: profile.persona ?? "", disabled: !canEdit || busy },
        system_prompt: { name: t("agentSystemPrompt"), type: "textarea", value: profile.system_prompt, disabled: !canEdit || busy },
        reasoning_effort: { name: t("agentReasoningEffort"), type: "text", value: profile.reasoning_effort ?? "", disabled: !canEdit || busy },
        max_context_tokens: { name: t("agentContextTokens"), type: "number", value: profile.max_context_tokens, disabled: !canEdit || busy },
        max_parallel_items: { name: t("agentParallelItems"), type: "number", value: profile.max_parallel_items, disabled: !canEdit || busy },
        max_risk: { name: t("agentMaximumRisk"), type: "select", value: profile.max_risk, disabled: !canEdit || busy, config: { options: ["LOW", "MEDIUM", "HIGH"].map(value => ({ value, label: value })) } },
        mcp_allowlist: { name: t("agentMCPAllowlist"), type: "textarea", value: profile.mcp_allowlist.join("\n"), disabled: !canEdit || busy },
        capability_tags: { name: t("agentCapabilityTags"), type: "textarea", value: profile.capability_tags.join("\n"), disabled: !canEdit || busy },
        default_channel_names: { name: t("agentDefaultChannels"), type: "textarea", value: profile.default_channel_names.join("\n"), disabled: !canEdit || busy },
        memory_policy: { name: t("agentMemoryPolicy"), type: "textarea", value: JSON.stringify(profile.memory_policy, null, 2), disabled: !canEdit || busy },
        auto_restart: { name: t("agentAutoRestart"), type: "switch", value: profile.auto_restart, disabled: !canEdit || busy },
      }} onSubmit={async values => {
        if (!canEdit || busy) return
        setBusy(true); setError("")
        try {
          const lines = (value: unknown) => String(value ?? "").split("\n").map(value => value.trim()).filter(Boolean)
          const patch: ProfilePatch = { version: profile.version, name: String(values.name), persona: String(values.persona ?? ""), system_prompt: String(values.system_prompt), max_context_tokens: Number(values.max_context_tokens), max_parallel_items: Number(values.max_parallel_items), max_risk: String(values.max_risk), capability_tags: lines(values.capability_tags), default_channel_names: lines(values.default_channel_names), memory_policy: JSON.parse(String(values.memory_policy)) as Record<string, unknown>, auto_restart: Boolean(values.auto_restart) }
          if (String(values.reasoning_effort ?? "") !== (profile.reasoning_effort ?? "")) patch.reasoning_effort = String(values.reasoning_effort ?? "")
          const mcp = lines(values.mcp_allowlist)
          if (JSON.stringify(mcp) !== JSON.stringify(profile.mcp_allowlist)) patch.mcp_allowlist = mcp
          if (selected) { patch.avatar_style = selected.style; patch.avatar_seed = selected.seed }
          const body = JSON.stringify(patch)
          if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
          setProfile(await profileApi.update(tenant, agent, patch, attempt.current!.key)); attempt.current = null; setSelected(null)
        } catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) } finally { setBusy(false) }
      }} />
    </>}
  </section>
}
