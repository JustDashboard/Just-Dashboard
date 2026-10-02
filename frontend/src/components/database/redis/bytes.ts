import type { RedisBytes } from "@/components/database/redis/types"

/**
 * Redis names and values are bytes, not text.
 *
 * The server sends each one as a string where the bytes are valid UTF-8 and
 * as `{ base64 }` where they are not, and takes either form back. Everything
 * here keeps that promise on the page: a value that arrived as bytes is
 * never passed through a text box (which would write U+FFFD over it), a name
 * is compared and keyed by its bytes, and what is drawn for something that is
 * not text is its bytes written out, never a guess at what they spell.
 */

/** Whether the bytes are not text. */
export function isBinary(value: RedisBytes): value is { base64: string } {
  return typeof value !== "string"
}

/** A stable identity for a name: two names are the same key when these are equal. */
export function bytesId(value: RedisBytes): string {
  return typeof value === "string" ? `t:${value}` : `b:${value.base64}`
}

export function sameBytes(a: RedisBytes | undefined, b: RedisBytes | undefined): boolean {
  if (a === undefined || b === undefined) return a === b
  return bytesId(a) === bytesId(b)
}

/** The bytes themselves. */
export function toBytes(value: RedisBytes): Uint8Array {
  if (typeof value === "string") return new TextEncoder().encode(value)
  const binary = atob(value.base64)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
  return out
}

/** Bytes as the server takes them: text where they are text, base64 where not. */
export function fromBytes(bytes: Uint8Array): RedisBytes {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes)
  } catch {
    let binary = ""
    // In runs: one `fromCharCode` call per byte is slow, and one call for a
    // megabyte overflows the argument list.
    for (let i = 0; i < bytes.length; i += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
    }
    return { base64: btoa(binary) }
  }
}

export function byteLength(value: RedisBytes): number {
  return toBytes(value).length
}

/** Several windows of one value, in order, as one. */
export function joinBytes(parts: readonly RedisBytes[]): RedisBytes {
  if (parts.every((part) => typeof part === "string")) return (parts as string[]).join("")
  const chunks = parts.map(toBytes)
  const out = new Uint8Array(chunks.reduce((n, chunk) => n + chunk.length, 0))
  let at = 0
  for (const chunk of chunks) {
    out.set(chunk, at)
    at += chunk.length
  }
  return fromBytes(out)
}

const HEX = "0123456789abcdef"

function escaped(byte: number): string {
  return `\\x${HEX[byte >> 4]}${HEX[byte & 15]}`
}

/**
 * A name or a member on one line, as redis-cli would print it: text as it
 * is, with what cannot be seen (a NUL, a tab, a line break) and what is not
 * text at all written as `\xHH`. It is for reading; what is sent back is the
 * `RedisBytes` it was drawn from.
 */
export function bytesLabel(value: RedisBytes): string {
  let out = ""
  if (typeof value === "string") {
    for (const char of value) {
      const code = char.charCodeAt(0)
      out += char.length === 1 && (code < 0x20 || code === 0x7f) ? escaped(code) : char
    }
    return out
  }
  for (const byte of toBytes(value)) {
    out += byte >= 0x20 && byte < 0x7f && byte !== 0x5c ? String.fromCharCode(byte) : escaped(byte)
  }
  return out
}

/** The bytes as lower-case hex pairs, with nothing between them. */
export function toHex(bytes: Uint8Array): string {
  let out = ""
  for (const byte of bytes) out += HEX[byte >> 4] + HEX[byte & 15]
  return out
}

/**
 * Hex typed by a person, as bytes: pairs with any spacing between them, and
 * `null` for anything else — an odd digit left over, a letter past f.
 */
export function parseHex(text: string): Uint8Array | null {
  const digits = text.replace(/\s+/g, "")
  if (digits.length % 2 !== 0 || /[^0-9a-f]/i.test(digits)) return null
  const out = new Uint8Array(digits.length / 2)
  for (let i = 0; i < out.length; i++) out[i] = parseInt(digits.slice(i * 2, i * 2 + 2), 16)
  return out
}

export type HexLine = { offset: string; hex: string; text: string }

/** A value as a hex dump: sixteen bytes a line, their offset before and their text after. */
export function hexLines(bytes: Uint8Array, from = 0, to = bytes.length): HexLine[] {
  const lines: HexLine[] = []
  const end = Math.min(to, bytes.length)
  for (let at = Math.max(from - (from % 16), 0); at < end; at += 16) {
    const row = bytes.subarray(at, Math.min(at + 16, bytes.length))
    let hex = ""
    let text = ""
    row.forEach((byte, i) => {
      hex += (i === 8 ? "  " : i ? " " : "") + HEX[byte >> 4] + HEX[byte & 15]
      text += byte >= 0x20 && byte < 0x7f ? String.fromCharCode(byte) : "."
    })
    lines.push({ offset: at.toString(16).padStart(8, "0"), hex, text })
  }
  return lines
}

/** The query that names a key: text where it is text, base64 where it is not — or is empty. */
export function keyQuery(key: RedisBytes): { key: string } | { keyB64: string } {
  if (typeof key !== "string") return { keyB64: key.base64 }
  // The key named "" exists, and `key=` would read as no key at all.
  return key === "" ? { keyB64: "" } : { key }
}

/** The same for a namespace of the tree. */
export function prefixQuery(prefix: RedisBytes): { prefix?: string; prefixB64?: string } {
  if (typeof prefix !== "string") return { prefixB64: prefix.base64 }
  return prefix === "" ? {} : { prefix }
}

/** Whether a pattern is a glob rather than one key's name. */
export function hasGlob(pattern: string): boolean {
  return /[*?[\\]/.test(pattern)
}

/** A name as a pattern that matches only itself. */
export function globEscape(name: string): string {
  return name.replace(/[*?[\]\\]/g, "\\$&")
}

/**
 * Which key an address names. A text name travels as `key`; one that is not
 * text as `keyB64`; and the key whose name is the empty string — which Redis
 * has, and which no query value can say — as `keyB64=-`.
 */
export function keyFromAddress(key: string, keyB64: string): RedisBytes | undefined {
  if (key !== "") return key
  if (keyB64 === EMPTY_KEY) return ""
  if (keyB64 === "") return undefined
  return /^[A-Za-z0-9+/]+={0,2}$/.test(keyB64) && keyB64.length % 4 === 0
    ? { base64: keyB64 }
    : undefined
}

const EMPTY_KEY = "-"

/** The address of a key, or of none. */
export function keyToAddress(key: RedisBytes | undefined): {
  key: string | null
  keyB64: string | null
} {
  if (key === undefined) return { key: null, keyB64: null }
  if (typeof key !== "string") return { key: null, keyB64: key.base64 }
  return key === "" ? { key: null, keyB64: EMPTY_KEY } : { key, keyB64: null }
}

export type TextShape = {
  /** Every character comes back from a text box as it went in. */
  plain: boolean
  /** Its lines end in CR LF, every one of them: what a save puts back. */
  crlf: boolean
}

/**
 * What a text box would do to a text.
 *
 * Valid UTF-8 is not yet text a person can edit: a box shows nothing for a
 * NUL or an escape, and hands every carriage return back as a line feed. So
 * text is `plain` only when it holds no control character but the tab and
 * the line break — and its line breaks are of one kind, since a box that
 * normalised a mix of them could not say which were which. Text that is not
 * plain is read and edited as its bytes.
 */
export function textShape(text: string): TextShape {
  let crlf = 0
  let lf = 0
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i)
    if (code === 0x0d && text.charCodeAt(i + 1) === 0x0a) {
      crlf++
      i++
    } else if (code === 0x0a) {
      lf++
    } else if ((code < 0x20 && code !== 0x09) || code === 0x7f) {
      return { plain: false, crlf: false }
    }
  }
  if (crlf > 0 && lf > 0) return { plain: false, crlf: false }
  return { plain: true, crlf: crlf > 0 }
}

/** Text a multi-line box hands back unchanged: no control characters, no carriage returns. */
export function boxSafe(value: RedisBytes | undefined): value is string {
  if (typeof value !== "string") return false
  const shape = textShape(value)
  return shape.plain && !shape.crlf
}

/** Text a one-line field hands back unchanged: the same, on one line. */
export function lineSafe(value: RedisBytes | undefined): value is string {
  return boxSafe(value) && !value.includes("\n")
}

/** Text that is a JSON object or array, as far as its two ends say. */
export function looksLikeJson(text: string): boolean {
  const trimmed = text.trim()
  if (trimmed.length < 2) return false
  const ends = trimmed[0] + trimmed[trimmed.length - 1]
  if (ends !== "{}" && ends !== "[]") return false
  try {
    JSON.parse(trimmed)
    return true
  } catch {
    return false
  }
}
