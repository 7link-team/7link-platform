import { test, expect } from "bun:test";
import { initial } from "../src/data";
import { hydrateChannels, visibleChannelMessages } from "../src/channels";
import { externalBridgeCommandOn, externalBridgeProblem, visibleExternalBridgeBindings } from "../src/external-bridge";
const fixture = () => { const s = structuredClone(initial); s.activeTenant = "studio"; hydrateChannels(s); return s; };
const channel = s => s.channels.find(c => c.id === "external");
const command = (s, action, fields = {}) => externalBridgeCommandOn(s, { tenantId: s.activeTenant, channelId: "external", revision: channel(s).externalBridge?.revision || 0, action, ...fields });
const bind = (s, fields = {}) => command(s, "save", { connectorId: "CN-01", externalChannelId: "C-CUSTOMER", name: "客户讨论", direction: "双向", ...fields });
const draft = (s, fields = {}) => command(s, "draft", { text: "已收到，明天反馈。", ...fields });
const authorization = d => ({ draftId: d.id, expectedText: d.text, confirmed: true });
const reject = (s, fn) => { const before = JSON.stringify(s); expect(fn).toThrow(); expect(JSON.stringify(s)).toBe(before); };

test("binding never imports history; receiving stays in this channel and deduplicates fixed external IDs", () => {
  const s = fixture(), before = structuredClone(s.messages); bind(s); expect(s.messages).toEqual(before);
  const input = { externalMessageId: "event-1", text: "外部固定正文" };
  const received = command(s, "receive", input), again = command(s, "receive", input);
  expect(again).toEqual({ messageId: received.messageId, duplicate: true });
  expect(s.messages).toHaveLength(before.length + 1);
  const m = s.messages.find(m => m.id === received.messageId);
  expect(m.channel).toBe("external");
  expect(m.source).toEqual({ platform: "Slack", connectorId: "CN-01", externalChannelId: "C-CUSTOMER", externalMessageId: "event-1", mode: "LOCAL_SIMULATION" });
  expect(visibleChannelMessages(s, "external").map(m => m.id)).toContain(m.id);
  for (const id of ["team", "private", "launch"]) expect(visibleChannelMessages(s, id).map(m => m.id)).not.toContain(m.id);
  reject(s, () => command(s, "receive", { ...input, text: "conflicting content" }));
  bind(s); expect(command(s, "receive", input).duplicate).toBe(true);
});

test("Human authorization is separate from draft and simulated receipt never claims delivery", () => {
  const s = fixture(); bind(s); const d = draft(s);
  reject(s, () => command(s, "send", authorization(d)));
  reject(s, () => command(s, "authorize", { ...authorization(d), confirmed: false }));
  reject(s, () => command(s, "authorize", { ...authorization(d), expectedText: "replaced" }));
  command(s, "authorize", authorization(d));
  const authorized = channel(s).externalOutbox[0]; expect(authorized.authorization.actorId).toBe("H-lin");
  const result = command(s, "send", authorization(d));
  expect(result.status).toBe("SIMULATED"); expect(result.receipt.delivered).toBe(false); expect(result.receipt.mode).toBe("LOCAL_SIMULATION");
  expect(command(s, "send", authorization(d)).receipt.id).toBe(result.receipt.id);
  expect(channel(s).externalBridgeHistory.filter(h => h.action === "模拟发送回执")).toHaveLength(1);
  expect(JSON.parse(JSON.stringify(channel(s)))).toEqual(channel(s));
});

test("pause resume edit revoke and connector changes invalidate old authorization without dropping history", () => {
  for (const action of ["pause", "save", "revoke", "connector"]) {
    const s = fixture(); bind(s); const d = draft(s); command(s, "authorize", authorization(d));
    const before = structuredClone(channel(s).externalOutbox);
    if (action === "save") bind(s, { externalChannelId: "C-NEW" });
    else if (action === "connector") s.resources.connectors.find(c => c.id === "CN-01").status = "未连接";
    else command(s, action);
    reject(s, () => command(s, "send", authorization(d)));
    expect(channel(s).externalOutbox).toEqual(before);
    if (["pause", "revoke"].includes(action)) {
      reject(s, () => draft(s));
      reject(s, () => command(s, "receive", { externalMessageId: "blocked-event", text: "blocked" }));
    }
    if (action === "pause") { command(s, "resume"); reject(s, () => command(s, "send", authorization(d))); }
    if (action === "revoke") { expect(channel(s).externalBridgeHistory.at(-1).previous.status).toBe("ACTIVE"); bind(s); reject(s, () => command(s, "send", authorization(d))); }
  }
});

test("tenant private execution membership archives and offline guards fail atomically", () => {
  for (const fields of [{ tenantId: "other" }, { channelId: "private" }, { channelId: "team" }, { channelId: "exec-W-102" }, { channelId: "missing" }, { revision: 9 }]) {
    const s = fixture(); reject(s, () => bind(s, fields));
  }
  for (const mutate of [s => {s.role = "external";}, s => {s.offline = true;}, s => {channel(s).archivedAt = "2026-10-14";}, s => {channel(s).members = [];}, s => {channel(s).shared = false;}]) {
    const s = fixture(); bind(s); mutate(s);
    reject(s, () => command(s, "receive", { externalMessageId: "secret", text: "secret" }));
    reject(s, () => draft(s));
  }
  const s = fixture(); bind(s); expect(visibleExternalBridgeBindings(s, "CN-01")).toHaveLength(1);
  s.role = "external"; expect(visibleExternalBridgeBindings(s, "CN-01")).toEqual([]); expect(externalBridgeProblem(s, "external")).toBeTruthy();
});

test("Agent may draft only as an actual channel member and cannot authorize or send", () => {
  const s = fixture(); bind(s);
  reject(s, () => draft(s, { actorId: "atlas" }));
  channel(s).members.push({ principalId: "atlas", role: "MEMBER", leftAt: null });
  const d = draft(s, { actorId: "atlas" }); expect(d.authorKind).toBe("Agent");
  for (const action of ["authorize", "send", "pause", "save", "receive"]) reject(s, () => command(s, action, { ...authorization(d), actorId: "atlas" }));
  command(s, "authorize", authorization(d)); expect(command(s, "send", authorization(d)).receipt.delivered).toBe(false);
  s.agents.find(a => a.id === "atlas").status = "退役"; reject(s, () => draft(s, { actorId: "atlas" }));
  reject(s, () => draft(s, { actorId: "H-zhou" }));
});

test("connector capability and disconnected state bound both receive and outbound paths", () => {
  const s = fixture(); bind(s, { connectorId: "CN-03", direction: "通知" });
  reject(s, () => draft(s)); reject(s, () => command(s, "receive", { externalMessageId: "x", text: "x" }));
  s.resources.connectors.find(c => c.id === "CN-03").status = "已连接";
  expect(draft(s).status).toBe("DRAFT"); reject(s, () => command(s, "receive", { externalMessageId: "x", text: "x" }));
  reject(s, () => bind(s, { connectorId: "CN-03", direction: "双向" }));
  bind(s, { direction: "只接收" }); reject(s, () => draft(s));
  expect(command(s, "receive", { externalMessageId: "y", text: "y" }).duplicate).toBe(false);
  s.resources.connectors.find(c => c.id === "CN-01").direction = "通知";
  reject(s, () => command(s, "receive", { externalMessageId: "z", text: "z" }));
});

test("duplicate targets cannot silently fan out into another shared channel", () => {
  const s = fixture(); bind(s); const second = structuredClone(channel(s)); second.id = "external-two"; delete second.externalBridge; s.channels.push(second);
  reject(s, () => bind(s, { channelId: second.id, revision: 0 }));
  command(s, "revoke"); expect(bind(s, { channelId: second.id, revision: 0 }).status).toBe("ACTIVE");
});
