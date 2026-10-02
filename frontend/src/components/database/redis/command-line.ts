import { toBytes } from "@/components/database/redis/bytes"
import type { RedisBytes } from "@/components/database/redis/types"

const HEX = "0123456789abcdef"
const hex = (byte: number) => `\\x${HEX[byte >> 4]}${HEX[byte & 15]}`

/**
 * One argument of a command line, written so the console's reader takes it
 * back as exactly these bytes: between double quotes, with the quote and the
 * backslash escaped and everything that is not printable ASCII as `\xHH`.
 *
 * A name is quoted whatever it holds. `user profile` unquoted is two
 * arguments, and a field named `FIELDS` or `"` would otherwise be read as
 * part of the command around it.
 */
export function quoteWord(word: RedisBytes | number): string {
  if (typeof word === "number") return String(word)
  let out = '"'
  for (const byte of toBytes(word)) {
    if (byte === 0x22 || byte === 0x5c) out += `\\${String.fromCharCode(byte)}`
    else if (byte >= 0x20 && byte < 0x7f) out += String.fromCharCode(byte)
    else out += hex(byte)
  }
  return `${out}"`
}

/**
 * A command as one line for `POST /redis/command`: the command's own words
 * as they are, every argument quoted. It is how the page runs the few
 * commands that have no route of their own — a field's expiry, a claim on a
 * pending entry — with the server classifying the line like any other.
 */
export function commandLine(command: string, args: readonly (RedisBytes | number)[]): string {
  return [command, ...args.map(quoteWord)].join(" ")
}
