import { describe, expect, it } from "bun:test"
import { ApiError, businessCode } from "../lib/iam-api"
import { loginError, sessionFailureStatus } from "./auth"

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

describe("sign-in step failures", () => {
  const captcha = new ApiError(
    422,
    "captcha_invalid",
    "captcha invalid",
    undefined,
    businessCode.captchaInvalid
  )
  it("names the step's own message for an unauthorized answer", () => {
    const unauthorized = new ApiError(401, "unauthorized")
    expect(loginError(unauthorized)).toBe("invalidCredentials")
    expect(loginError(unauthorized, "invalidPhoneCode")).toBe(
      "invalidPhoneCode"
    )
  })
  // Asking for a phone code is guarded by the captcha, so a rejected answer has
  // to send the person back to the image rather than away from the form.
  it("asks for the captcha again when the challenge was rejected", () => {
    expect(loginError(captcha)).toBe("invalidCaptcha")
    expect(loginError(captcha, "invalidChallenge")).toBe("invalidCaptcha")
  })
  it("reads anything else as the identity service being unavailable", () => {
    expect(loginError(new ApiError(503, "unavailable"))).toBe(
      "identityUnavailable"
    )
    expect(loginError(new TypeError("Failed to fetch"))).toBe(
      "identityUnavailable"
    )
  })
})
