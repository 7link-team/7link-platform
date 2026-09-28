import { describe, expect, it } from "bun:test"
import { ApiError } from "../lib/iam-api"
import { sessionFailureStatus } from "./auth"

describe("session verification failures", () => {
  it("treats only an explicit unauthorized response as an anonymous session", () => {
    expect(sessionFailureStatus(new ApiError(401, "unauthorized"))).toBe(
      "anonymous"
    )
    for (const failure of [
      new TypeError("Failed to fetch"),
      new ApiError(503, "unavailable"),
      new ApiError(403, "forbidden"),
      new ApiError(200, "invalid_response"),
    ]) {
      expect(sessionFailureStatus(failure)).toBe("error")
    }
  })
})
