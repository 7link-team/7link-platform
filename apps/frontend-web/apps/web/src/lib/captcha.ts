import { iamApi, type Captcha } from "./iam-api"

/**
 * Ask for a challenge the browser can actually draw.
 *
 * A deployment may offer the interactive kinds as well, and the forms here can
 * only present the character image, so a challenge of another kind is discarded
 * and another one requested rather than leaving somebody at a blank box. The
 * attempts are bounded because a deployment that offers no text challenge at
 * all would otherwise spin.
 */
async function issueTextCaptcha(
  issue: () => Promise<Captcha>
): Promise<Captcha> {
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const issued = await issue()
    if (issued.type === "text") return issued
  }
  throw new Error("no text captcha available")
}

export const issueLoginCaptcha = () =>
  issueTextCaptcha(iamApi.createLoginCaptcha)

export const issueRegistrationCaptcha = () =>
  issueTextCaptcha(iamApi.createRegistrationCaptcha)
