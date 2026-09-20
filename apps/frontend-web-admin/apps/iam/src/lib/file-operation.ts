import type {
  FileOperationConflicts,
  FileOperationDecision,
  FileOperationInput,
  FileOperationPolicy,
} from "./iam-api"
import { ApiError, fileOperationConflicts } from "./iam-api"

export type PendingFileOperation = {
  input: FileOperationInput
  idempotencyKey: string
  frontier: FileOperationConflicts
  resolvedDecisions: FileOperationDecision[]
  decisions: Record<string, FileOperationPolicy | "">
}

export function baseFileOperationInput(
  input: FileOperationInput
): FileOperationInput {
  return {
    action: input.action,
    destination_id: input.destination_id,
    sources: input.sources,
    ...(input.file_policy ? { file_policy: input.file_policy } : {}),
    ...(input.directory_policy
      ? { directory_policy: input.directory_policy }
      : {}),
    ...(input.type_policy ? { type_policy: input.type_policy } : {}),
  }
}

export function fileOperationIdentity(input: FileOperationInput): string {
  return JSON.stringify(baseFileOperationInput(input))
}

export function pendingFileOperation(
  input: FileOperationInput,
  idempotencyKey: string,
  frontier: FileOperationConflicts,
  priorDecisions: readonly FileOperationDecision[] = input.decisions ?? []
): PendingFileOperation {
  const priorByKey = new Map(
    priorDecisions.map((decision) => [decision.conflict_key, decision.policy])
  )
  const currentKeys = new Set(
    frontier.conflicts.map((conflict) => conflict.key)
  )
  return {
    input: baseFileOperationInput(input),
    idempotencyKey,
    frontier,
    resolvedDecisions: priorDecisions.filter(
      (decision) => !currentKeys.has(decision.conflict_key)
    ),
    decisions: Object.fromEntries(
      frontier.conflicts.map((conflict) => {
        const prior = priorByKey.get(conflict.key)
        return [
          conflict.key,
          prior && conflict.policies.includes(prior) ? prior : "",
        ]
      })
    ),
  }
}

export function confirmedFileOperation(
  pending: PendingFileOperation
): FileOperationInput | null {
  const decisions = pending.frontier.conflicts.map((conflict) => ({
    conflict_key: conflict.key,
    policy: pending.decisions[conflict.key],
  }))
  if (decisions.some((decision) => !decision.policy)) return null
  return {
    ...pending.input,
    observed_revision: pending.frontier.space_revision,
    decisions: [
      ...pending.resolvedDecisions,
      ...(decisions as FileOperationDecision[]),
    ],
  }
}

type FileOperationExecution =
  | { kind: "success" }
  | { kind: "conflict"; pending: PendingFileOperation }
  | { kind: "error"; cause: unknown; definitive: boolean }

export async function executeFileOperation(
  create: (
    input: FileOperationInput,
    idempotencyKey: string
  ) => Promise<unknown>,
  input: FileOperationInput,
  idempotencyKey: string
): Promise<FileOperationExecution> {
  let request = input
  let restartedPreflight = false
  for (;;) {
    try {
      await create(request, idempotencyKey)
      return { kind: "success" }
    } catch (cause) {
      if (
        !restartedPreflight &&
        request.observed_revision &&
        cause instanceof ApiError &&
        cause.code === "file_state_changed"
      ) {
        request = baseFileOperationInput(input)
        restartedPreflight = true
        continue
      }
      const frontier = fileOperationConflicts(cause)
      if (frontier) {
        return {
          kind: "conflict",
          pending: pendingFileOperation(
            input,
            idempotencyKey,
            frontier,
            input.decisions
          ),
        }
      }
      return {
        kind: "error",
        cause,
        definitive: cause instanceof ApiError,
      }
    }
  }
}
