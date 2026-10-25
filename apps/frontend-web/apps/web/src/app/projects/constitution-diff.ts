export type ConstitutionDiffRow = {
  id: number
  kind: "unchanged" | "removed" | "added"
  before: number | null
  after: number | null
  text: string
}

// Retain common context around a replaced block. Linear time and space even
// for the maximum document size; every source line remains reconstructible.
export function constitutionDiff(
  before: string,
  after: string
): ConstitutionDiffRow[] {
  const left = before === "" ? [] : before.split("\n")
  const right = after === "" ? [] : after.split("\n")
  let start = 0
  while (
    start < Math.min(left.length, right.length) &&
    left[start] === right[start]
  )
    start++
  let end = 0
  while (
    end < Math.min(left.length, right.length) - start &&
    left[left.length - end - 1] === right[right.length - end - 1]
  )
    end++
  const rows: ConstitutionDiffRow[] = []
  const append = (
    kind: ConstitutionDiffRow["kind"],
    before: number | null,
    after: number | null,
    text: string
  ) => {
    rows.push({ id: rows.length, kind, before, after, text })
  }
  for (let i = 0; i < start; i++) append("unchanged", i + 1, i + 1, left[i]!)
  for (let i = start; i < left.length - end; i++)
    append("removed", i + 1, null, left[i]!)
  for (let i = start; i < right.length - end; i++)
    append("added", null, i + 1, right[i]!)
  for (let i = 0; i < end; i++)
    append(
      "unchanged",
      left.length - end + i + 1,
      right.length - end + i + 1,
      left[left.length - end + i]!
    )
  return rows
}
