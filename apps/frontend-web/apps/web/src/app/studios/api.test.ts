import { afterEach, expect, spyOn, test } from "bun:test"
import { iamApi } from "../../lib/iam-api"
import { novelApi, videoApi, studiosApi, type StudioDraft, type StudioRecord } from "./api"
let restore: (() => void) | undefined
afterEach(() => restore?.())
test("studio commands preserve scope, exact version and retry key", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const signal = new AbortController().signal
 const draft: StudioDraft = {domain: "VIDEO", kind: "SHOT", content: {title: "Opening", text: "", source: "", duration_ms: 3000, provider: "", model: "", seed: ""}}
 await studiosApi.create("7", "99", draft, "stable-retry", signal)
 await studiosApi.create("7", "99", draft, "stable-retry", signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/7/projects/99/studio-records"});expect(calls[0]?.init?.signal).toBe(signal)
 await studiosApi.decide("7", "99", {...draft, id: "123", binding_id: "8", state: "OPEN", version: 4, created_by: "11", created_at: ""} as StudioRecord, "SELECT", "Human review", "decision-retry")
 expect(JSON.parse(calls.at(-1)?.init?.body as string)).toEqual({version: 4, action: "SELECT", reason: "Human review"})
 expect(calls.at(-1)?.init?.headers).toEqual({"Idempotency-Key": "decision-retry"})
 await studiosApi.list("7", "99", "NOVEL", "123", signal)
 expect(calls.at(-1)?.route).toEqual({method: "GET", url: "/tenants/7/projects/99/studio-records?domain=NOVEL&limit=50&sort=id&cursor=123"})
})
test("team board preserves page scope and exact CAS/idempotent decision payload", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const signal = new AbortController().signal
 await studiosApi.team("7", "99", "123", signal)
 expect(calls[0]?.route).toEqual({method: "GET", url: "/tenants/7/projects/99/team-task-board?limit=50&sort=id&cursor=123"})
 const body = {version: 4, action: "SIGN_OFF" as const, reason: "Independent human scope", content: {title: "Report", text: "", source: "", duration_ms: 0, provider: "", model: "", seed: ""}}
 await studiosApi.teamEdit("7", "99", "123", body, "stable-signature", signal)
 expect(calls[1]?.route).toEqual({method: "POST", url: "/tenants/7/projects/99/team-task-board/123/decisions"})
 expect(calls[1]?.init?.headers).toEqual({"Idempotency-Key": "stable-signature"})
 expect(JSON.parse(calls[1]?.init?.body as string)).toEqual(body)
 expect(calls[1]?.init?.signal).toBe(signal)
})

test("novel and video commands use null optional IDs without changing text, seed or real IDs", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const signal = new AbortController().signal
 const content = {title: "0", text: "", source: "", duration_ms: 3000, provider: "", model: "", seed: "0"}
 const novel = {version: 0, action: "SAVE" as const, kind: "STORY_BIBLE", reason: "", content: {...content, novel: {entries: [], characters: [], beats: [{name: "0", target_percent: 50, chapter_id: "0"}, {name: "Real", target_percent: 90, chapter_id: "123"}], foreshadow: {planted: "123", planned: "124", resolved: "", retained: false, reason: "0"}}}}
 await novelApi.edit("7", "99", "", novel, "novel-retry", signal)
 const body = JSON.parse(calls[0]?.init?.body as string)
 expect(body.content.novel.beats.map((b: {chapter_id: string | null}) => b.chapter_id)).toEqual([null, "123"])
 expect(body.content.novel.foreshadow).toEqual({planted: "123", planned: "124", resolved: null, retained: false, reason: "0"})
 expect(body.content.seed).toBe("0"); expect(body.content.title).toBe("0")
 expect(novel.content.novel.beats[0]?.chapter_id).toBe("0")
 const video = {action: "SAVE" as const, version: 0, parent_id: "0", reason: "Author", content: {...content, video: {role: "SHOT" as const, character: "0", framing: "", execution_id: "", generation_id: "0", evidence_id: "123", output_index: 0}}}
 await videoApi.edit("7", "99", "", video, "video-retry", signal)
 const shot = JSON.parse(calls[1]?.init?.body as string)
 expect(shot.parent_id).toBeNull()
 expect(shot.content.video).toEqual({...video.content.video, execution_id: null, generation_id: null})
 expect(shot.content.seed).toBe("0"); expect(video.parent_id).toBe("0")
 for (const [i, key] of ["novel-retry", "video-retry"].entries()) {
  expect(calls[i]?.init?.signal).toBe(signal)
  expect(calls[i]?.init?.headers).toEqual({"Idempotency-Key": key})
 }
 await videoApi.edit("7", "99", "125", {...video, parent_id: "124"}, "video-real", signal)
 expect(JSON.parse(calls[2]?.init?.body as string).parent_id).toBe("124")
})
