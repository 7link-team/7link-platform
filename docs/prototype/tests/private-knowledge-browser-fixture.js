globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
const api = await import("../src/store.js");
api.reset();
api.saveResource("knowledge", { name: "本人私人资料测试", owner: "林予", scope: "私人", status: "待确认", kind: "记忆", version: "v1", source: "本人测试输入", content: "本人最初正文" });
let own = api.read().resources.knowledge.find(record => record.name === "本人私人资料测试");
api.saveResource("knowledge", { ...own, content: "本人修订正文" });
own = api.read().resources.knowledge.find(record => record.id === own.id);
const other = api.read().resources.knowledge.find(record => record.id === "K-03");
api.update(state => {
  // Historical event shapes reproduced from pre-repair saveResource/resourceCommand.
  const legacyEvents = [
    { id: "legacy-private-status", time: "2026-10-14T00:00:00Z", actor: "林予", text: `${other.name} → 已禁用 · 私人原因`, target: `/knowledge/${other.id}` },
    { id: "legacy-private-save", time: "2026-10-14T00:00:00Z", actor: "林予", text: `更新${other.name}`, target: "/knowledge" },
  ];
  state.events.unshift(...legacyEvents);
  state.testPrivateKnowledge = { ownId: own.id, ownName: own.name, otherId: other.id, otherName: other.name, otherContent: other.content, legacyEvents };
});
console.log(JSON.stringify(api.read()));
