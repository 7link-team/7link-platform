import { describe, expect, it } from "bun:test"
import {
  ApiError,
  type FileOperationConflicts,
  type FileOperationDecision,
  type FileOperationInput,
} from "./iam-api"
import {
  confirmedFileOperation,
  executeFileOperation,
  fileOperationIdentity,
  pendingFileOperation,
} from "./file-operation"

const firstKey = "a".repeat(64)
const secondKey = "b".repeat(64)
const input: FileOperationInput = {
  action: "move",
  destination_id: "10",
  sources: ["11"],
}

function frontier(
  key: string,
  policies: FileOperationConflicts["conflicts"][number]["policies"],
  revision = 7
): FileOperationConflicts {
  return {
    space_revision: revision,
    conflicts: [
      {
        key,
        kind: "file",
        source: { kind: "file", name: "report.txt", revision: 2 },
        target: { kind: "file", name: "report.txt", revision: 3 },
        parent: { node_id: "10" },
        policies,
      },
    ],
  }
}

describe("file operation conflict state", () => {
  it("keeps one logical identity across preflight and decision retries", () => {
    expect(
      fileOperationIdentity({
        ...input,
        observed_revision: 7,
        decisions: [{ conflict_key: firstKey, policy: "skip" }],
      })
    ).toBe(fileOperationIdentity(input))
  })

  it("preserves prior choices only when the fresh server frontier permits them", () => {
    const prior: FileOperationDecision[] = [
      { conflict_key: firstKey, policy: "skip" },
    ]
    const preserved = pendingFileOperation(
      input,
      "stable-key",
      frontier(firstKey, ["overwrite", "skip"]),
      prior
    )
    expect(preserved.idempotencyKey).toBe("stable-key")
    expect(preserved.decisions[firstKey]).toBe("skip")
    expect(preserved.resolvedDecisions).toEqual([])
    expect(preserved.frontier.conflicts[0]?.policies).toEqual([
      "overwrite",
      "skip",
    ])

    const rejected = pendingFileOperation(
      input,
      "stable-key",
      frontier(firstKey, ["overwrite"]),
      prior
    )
    expect(rejected.decisions[firstKey]).toBe("")
  })

  it("accumulates resolved decisions across nested conflict frontiers", () => {
    const prior: FileOperationDecision[] = [
      { conflict_key: firstKey, policy: "skip" },
    ]
    const pending = pendingFileOperation(
      input,
      "stable-key",
      frontier(secondKey, ["overwrite", "rename"], 9),
      prior
    )
    pending.decisions[secondKey] = "rename"

    expect(confirmedFileOperation(pending)).toEqual({
      ...input,
      observed_revision: 9,
      decisions: [
        { conflict_key: firstKey, policy: "skip" },
        { conflict_key: secondKey, policy: "rename" },
      ],
    })
  })

  it("does not confirm while any current conflict lacks a choice", () => {
    expect(
      confirmedFileOperation(
        pendingFileOperation(input, "stable-key", frontier(firstKey, ["skip"]))
      )
    ).toBeNull()
  })

  it("restarts stale confirmation as preflight with the same key", async () => {
    const submitted: { input: FileOperationInput; key: string }[] = []
    const selected: FileOperationDecision = {
      conflict_key: firstKey,
      policy: "skip",
    }
    const fresh = frontier(firstKey, ["overwrite", "skip"], 8)
    const result = await executeFileOperation(
      async (request, key) => {
        submitted.push({ input: request, key })
        if (submitted.length === 1)
          throw new ApiError(409, "file_state_changed")
        throw new ApiError(409, "file_operation_conflict", undefined, fresh)
      },
      { ...input, observed_revision: 7, decisions: [selected] },
      "stable-key"
    )

    expect(submitted).toEqual([
      {
        input: { ...input, observed_revision: 7, decisions: [selected] },
        key: "stable-key",
      },
      { input, key: "stable-key" },
    ])
    expect(result).toMatchObject({
      kind: "conflict",
      pending: {
        idempotencyKey: "stable-key",
        frontier: fresh,
        decisions: { [firstKey]: "skip" },
      },
    })
  })

  it("distinguishes definitive API failures from ambiguous response loss", async () => {
    const networkFailure = await executeFileOperation(
      async () => {
        throw new TypeError("fetch failed")
      },
      input,
      "stable-key"
    )
    expect(networkFailure).toMatchObject({ kind: "error", definitive: false })

    const rejected = await executeFileOperation(
      async () => {
        throw new ApiError(403, "forbidden")
      },
      input,
      "stable-key"
    )
    expect(rejected).toMatchObject({ kind: "error", definitive: true })
  })
})
