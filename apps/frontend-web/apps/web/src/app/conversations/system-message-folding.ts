export type SystemMessage = { kind?: string; fold_key?: string; attention?: boolean; payload?: Record<string, unknown>; body: string | null }
function collapsible(message: SystemMessage) {
  if (message.attention || !message.fold_key) return false
  if (message.kind === "EVENT") return true
  return message.kind === "DISPATCH_CARD" && ["APPLIED", "OVERRIDDEN", "EXPIRED", "SELECTED", "DISMISSED"].includes(String(message.payload?.status))
}
// Input must be oldest first. Different kinds/keys and a gap break a group;
// attention and pending decisions always remain individually visible.
export function foldSystemMessages<T extends SystemMessage>(messages: T[]) {
  const groups: { latest: T; previous: T[] }[] = []
  for (const message of messages) {
    const group = groups[groups.length - 1]
    if (group && collapsible(message) && collapsible(group.latest) && group.latest.fold_key === message.fold_key && group.previous.length < 49) {
      group.previous.push(group.latest); group.latest = message
    } else groups.push({ latest: message, previous: [] })
  }
  return groups
}
