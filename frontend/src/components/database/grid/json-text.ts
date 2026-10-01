/**
 * JSON as text: laid out for reading and squeezed for comparing, without ever
 * being turned into a JavaScript value on the way.
 *
 * `JSON.parse` followed by `JSON.stringify` looks like a formatter and is a
 * rewrite. It reads `12345678901234567890` as a float and prints
 * `12345678901234567000`, moves the keys `"10"` and `"2"` ahead of `"b"`, and
 * keeps one of two keys that share a name — and a grid that opened a document
 * that way would write all of that back the moment the reader changed a single
 * field somewhere else in it. So the two functions here only ever move
 * whitespace: every token between two structural characters comes out exactly
 * as it went in.
 */

const WHITESPACE = " \t\n\r"
const STRUCTURAL = "{}[],:"

/** Whether the text is one JSON value. The only question `JSON.parse` is asked here. */
function parses(text: string): boolean {
  try {
    JSON.parse(text)
    return true
  } catch {
    return false
  }
}

/**
 * The tokens of a document that is known to parse: the six structural
 * characters, strings with their quotes and escapes untouched, and every
 * number, `true`, `false` and `null` as the run of characters it was written as.
 */
function tokens(text: string): string[] {
  const out: string[] = []
  let at = 0
  while (at < text.length) {
    const ch = text[at]
    if (WHITESPACE.includes(ch)) {
      at++
    } else if (STRUCTURAL.includes(ch)) {
      out.push(ch)
      at++
    } else if (ch === '"') {
      let end = at + 1
      // A backslash takes the character after it, so `\"` does not end the string.
      while (text[end] !== '"') end += text[end] === "\\" ? 2 : 1
      out.push(text.slice(at, end + 1))
      at = end + 1
    } else {
      let end = at + 1
      while (
        end < text.length &&
        !WHITESPACE.includes(text[end]) &&
        !STRUCTURAL.includes(text[end])
      ) {
        end++
      }
      out.push(text.slice(at, end))
      at = end
    }
  }
  return out
}

/**
 * The document on indented lines, two spaces a level, in the layout
 * `JSON.stringify(value, null, 2)` would give it. Null when the text is not JSON.
 */
export function indentJSON(text: string): string | null {
  if (!parses(text)) return null
  const list = tokens(text)
  let out = ""
  let depth = 0
  const line = () => `\n${"  ".repeat(depth)}`
  for (let i = 0; i < list.length; i++) {
    const token = list[i]
    if (token === "{" || token === "[") {
      // An empty object or array stays the two characters it is.
      if (list[i + 1] === (token === "{" ? "}" : "]")) {
        out += token + list[i + 1]
        i++
      } else {
        depth++
        out += token + line()
      }
    } else if (token === "}" || token === "]") {
      depth--
      out += line() + token
    } else if (token === ",") {
      out += token + line()
    } else if (token === ":") {
      out += ": "
    } else {
      out += token
    }
  }
  return out
}

/**
 * The document with the whitespace between its tokens removed, which is what
 * two spellings of it are compared by. Deliberately no cleverer than that:
 * `1.0` is not `1` and `"A"` is not `"A"` here. Calling two documents
 * different when they mean the same costs an UPDATE nobody needed; calling
 * them the same when they differ throws away the reader's correction.
 */
export function compactJSON(text: string): string | null {
  return parses(text) ? tokens(text).join("") : null
}
