/**
 * A pasted `.env`, read the way the import will read it, so the sheet can say
 * what pressing Import does before it is pressed.
 *
 * It is `parseDotenvEntries` in `backend/internal/deploy/configuration_store.go`
 * written again, line for line, rather than a looser reading of the same
 * format: a preview that accepted what the server then refused — or listed a
 * value the server would have cut at a quote — would be a preview of a
 * different import. The server stays the authority. When its dry run answers,
 * its verdicts replace these; this is what the sheet draws while that answer
 * is in flight and when it cannot be had.
 */

export type DotenvRefusal = "invalid_name" | "duplicate" | "invalid_value"

export type DotenvEntry = {
  name: string
  value: string
  /** The line the assignment starts on, counting from one. */
  line: number
  refused?: DotenvRefusal
}

export type DotenvReading = {
  entries: DotenvEntry[]
  /** A line the parser cannot read past, which refuses the whole paste. */
  error?: string
}

const MAX_BYTES = 256 * 1024
const MAX_VALUE = 64 * 1024
const MAX_ENTRIES = 256
/** `envKeyRe` in `backend/internal/deploy/model.go`, its 128-character cap included. */
export const NAME = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/
/** What a backslash means inside double quotes; anything else keeps its backslash. */
const ESCAPES: Record<string, string> = { n: "\n", r: "\r", t: "\t", "\\": "\\", '"': '"' }
const bytesOf = (text: string) => new TextEncoder().encode(text).length

export function readDotenv(raw: string): DotenvReading {
  if (bytesOf(raw) > MAX_BYTES) return { entries: [], error: "The paste is larger than 256 KiB." }
  const input = raw.replace(/\r\n/g, "\n").replace(/\r/g, "\n")
  const entries: DotenvEntry[] = []
  const seen = new Set<string>()
  const blank = (at: number) => input[at] === " " || input[at] === "\t"
  let offset = 0
  let line = 1
  for (; offset < input.length; line++) {
    const entryLine = line
    while (offset < input.length && blank(offset)) offset++
    if (offset >= input.length) break
    if (input[offset] === "\n") {
      offset++
      continue
    }
    if (input[offset] === "#") {
      while (offset < input.length && input[offset] !== "\n") offset++
      if (offset < input.length) offset++
      continue
    }
    if (input.startsWith("export ", offset)) offset += "export ".length
    const keyStart = offset
    while (offset < input.length && input[offset] !== "=" && input[offset] !== "\n") offset++
    if (offset >= input.length || input[offset] !== "=")
      return { entries, error: `Line ${line} needs NAME=value.` }
    const name = input.slice(keyStart, offset).trim()
    const entry: DotenvEntry = { name, value: "", line }
    if (!NAME.test(name)) entry.refused = "invalid_name"
    else if (seen.has(name)) entry.refused = "duplicate"
    seen.add(name)
    offset++
    while (offset < input.length && blank(offset)) offset++
    let value = ""
    const quote = input[offset]
    if (quote === "'" || quote === '"') {
      offset++
      let out = ""
      let closed = false
      while (offset < input.length) {
        const ch = input[offset]
        offset++
        if (ch === quote) {
          closed = true
          break
        }
        if (ch === "\n") line++
        if (quote === '"' && ch === "\\" && offset < input.length) {
          out += ESCAPES[input[offset]] ?? `\\${input[offset]}`
          offset++
          continue
        }
        out += ch
      }
      if (!closed) {
        entries.push(entry)
        return {
          entries,
          error: `The quoted value starting on line ${entryLine} is not closed.`,
        }
      }
      value = out
      while (offset < input.length && blank(offset)) offset++
      if (offset < input.length && input[offset] !== "\n" && input[offset] !== "#") {
        entries.push(entry)
        return { entries, error: `Line ${line} has text after its quoted value.` }
      }
      if (input[offset] === "#") while (offset < input.length && input[offset] !== "\n") offset++
    } else {
      const valueStart = offset
      while (offset < input.length && input[offset] !== "\n") offset++
      value = input.slice(valueStart, offset).trim()
    }
    if (!entry.refused && (bytesOf(value) > MAX_VALUE || value.includes("\u0000")))
      entry.refused = "invalid_value"
    entry.value = value
    entries.push(entry)
    if (input[offset] === "\n") offset++
    if (entries.length > MAX_ENTRIES)
      return { entries, error: "The paste holds more than 256 variables." }
  }
  return { entries }
}

/** Why a name was refused, in the words the sheet shows beside it. */
export const REFUSAL_WORD: Record<DotenvRefusal, string> = {
  invalid_name: "not a valid name",
  duplicate: "appears twice",
  invalid_value: "value too long",
}
