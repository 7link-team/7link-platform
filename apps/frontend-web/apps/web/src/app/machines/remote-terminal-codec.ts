/** A line console sends one Enter key, including ConPTY's CR convention. */
export function encodeTerminalLine(command: string, os: string): string {
  const bytes = new TextEncoder().encode(command + (os === "WINDOWS" ? "\r" : "\n"))
  return btoa(Array.from(bytes, (byte) => String.fromCharCode(byte)).join(""))
}

/** Render terminal data as inert text; remove OSC links and terminal controls. */
export function terminalPlainText(value: string): string {
  // eslint-disable-next-line no-control-regex -- Terminal control bytes are intentionally matched.
  return value.replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\|$)/gu, "")
    // eslint-disable-next-line no-control-regex -- Strip CSI, escape sequences, and incomplete final escapes.
    .replace(/\x1b(?:\[[0-?]*[ -/]*[@-~]?|[@-_])?/gu, "")
    // eslint-disable-next-line no-control-regex -- Keep newlines/tabs while removing nonprinting controls.
    .replace(/[\x00-\x08\x0b-\x1f\x7f]/gu, "")
}
