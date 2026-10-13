const octetStreamDataURL = /^data:application\/octet-stream;base64,/iu

export function normalizeOfficeEmbeddedImageURL(value: string) {
  const trimmed = value.trim()
  if (!octetStreamDataURL.test(trimmed)) return undefined

  const separator = trimmed.indexOf(",")
  const payload = trimmed.slice(separator + 1)
  const mimeType = officeImageMimeType(payload)
  return mimeType ? `data:${mimeType};base64,${payload}` : undefined
}

function officeImageMimeType(payload: string) {
  let header: Uint8Array
  try {
    const decoded = atob(payload.slice(0, 32))
    header = Uint8Array.from(decoded, (character) => character.charCodeAt(0))
  } catch {
    return undefined
  }

  if (hasBytes(header, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))
    return "image/png"
  if (hasBytes(header, [0xff, 0xd8, 0xff])) return "image/jpeg"
  if (hasASCII(header, 0, "GIF87a") || hasASCII(header, 0, "GIF89a"))
    return "image/gif"
  if (hasASCII(header, 0, "BM")) return "image/bmp"
  if (hasASCII(header, 0, "RIFF") && hasASCII(header, 8, "WEBP"))
    return "image/webp"
  return undefined
}

function hasBytes(bytes: Uint8Array, expected: number[]) {
  return expected.every((value, index) => bytes[index] === value)
}

function hasASCII(bytes: Uint8Array, offset: number, expected: string) {
  return [...expected].every(
    (character, index) => bytes[offset + index] === character.charCodeAt(0)
  )
}
