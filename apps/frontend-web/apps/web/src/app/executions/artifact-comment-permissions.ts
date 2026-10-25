import type { CommentMessage } from "./artifact-comment-conversation"

export function canEditArtifactComment(message: CommentMessage, actor: string, now = Date.now()) {
  return message.created_by === actor && !message.retracted_at && message.edit_count < 10 && now <= Date.parse(message.created_at) + 15 * 60 * 1000
}
