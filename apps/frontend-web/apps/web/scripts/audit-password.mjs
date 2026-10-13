import { readFileSync } from "node:fs"

/**
 * The password of the account the browser audits sign in with.
 *
 * A file is the preferred way in: the environment is inherited by every child
 * process, is printed by anything that dumps the environment on a crash, and
 * lands in shell history when it is set on the command line. A file can be kept
 * to one reader and deleted afterwards. `IAM_BROWSER_PASSWORD` stays supported
 * because a throwaway container has nowhere to put a file.
 */
export function auditPassword() {
  const file = process.env.IAM_BROWSER_PASSWORD_FILE
  if (file) {
    const value = readFileSync(file, "utf8").trim()
    if (!value) throw new Error(`${file} holds no password`)
    return value
  }
  const value = process.env.IAM_BROWSER_PASSWORD
  if (!value)
    throw new Error(
      "IAM_BROWSER_PASSWORD_FILE or IAM_BROWSER_PASSWORD is required"
    )
  return value
}
