import { useMatches } from "react-router"

/** A route opts into a bounded desktop workspace; other pages keep document flow. */
export function useWorkspaceLayout(): boolean {
  return useMatches().some(
    ({ handle }) =>
      typeof handle === "object" &&
      handle !== null &&
      "layout" in handle &&
      handle.layout === "workspace"
  )
}
