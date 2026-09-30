/**
 * The solved challenge an automated caller sends with a login or registration.
 *
 * Login presents a captcha on every attempt, so a script that drives the API
 * has to answer one. It answers with the value the service is configured to
 * accept for any challenge (`authn.captcha.test_answer`), which is why no image
 * has to be read and no challenge has to be issued first: that answer is
 * compared after the store lookup misses, so any non-empty id reaches it.
 *
 * The answer comes from the environment because it is a property of the
 * deployment being driven, not of this repository. A constant here would be a
 * bypass in every deployment that ever forgot to switch the mode off.
 */
export function auditCaptcha() {
  const answer = process.env.IAM_CAPTCHA_ANSWER
  if (!answer)
    throw new Error(
      "IAM_CAPTCHA_ANSWER is required: set it to the service's authn.captcha.test_answer"
    )
  return { captcha_type: "text", captcha_id: "automated", captcha_answer: answer }
}
