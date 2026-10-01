/**
 * A JSON document read without being turned into JavaScript values.
 *
 * `JSON.parse` reads `12345678901234567890` as a float and hands back
 * `12345678901234567000`, keeps one of two keys that share a name and moves
 * the numeric ones to the front. A document read that way and written back
 * is a different document — and the reader only changed one field of it. So
 * a document here is a tree of nodes that keep what was written: a number is
 * the digits it arrived as, an object is its pairs in their order.
 */
export type JsonNode =
  | { kind: "object"; entries: { key: string; value: JsonNode }[] }
  | { kind: "array"; items: JsonNode[] }
  | { kind: "string"; value: string }
  | { kind: "number"; raw: string }
  | { kind: "boolean"; value: boolean }
  | { kind: "null" }

const WHITESPACE = " \t\n\r"

/** The document as a tree, or `null` when the text is not one JSON value. */
export function parseJsonDoc(text: string): JsonNode | null {
  // The grammar is `JSON.parse`'s to judge; the walk below only has to read
  // a document already known to be well formed.
  try {
    JSON.parse(text)
  } catch {
    return null
  }
  let at = 0
  const skip = () => {
    while (at < text.length && WHITESPACE.includes(text[at])) at++
  }
  const string = (): string => {
    const start = at
    at++
    // A backslash takes the character after it, so `\"` does not end the string.
    while (text[at] !== '"') at += text[at] === "\\" ? 2 : 1
    at++
    return JSON.parse(text.slice(start, at)) as string
  }
  const value = (): JsonNode => {
    skip()
    const ch = text[at]
    if (ch === "{") {
      at++
      const entries: { key: string; value: JsonNode }[] = []
      skip()
      while (text[at] !== "}") {
        skip()
        const key = string()
        skip()
        at++ // the colon
        entries.push({ key, value: value() })
        skip()
        if (text[at] === ",") at++
        skip()
      }
      at++
      return { kind: "object", entries }
    }
    if (ch === "[") {
      at++
      const items: JsonNode[] = []
      skip()
      while (text[at] !== "]") {
        items.push(value())
        skip()
        if (text[at] === ",") at++
        skip()
      }
      at++
      return { kind: "array", items }
    }
    if (ch === '"') return { kind: "string", value: string() }
    const start = at
    while (at < text.length && !WHITESPACE.includes(text[at]) && !",]}".includes(text[at])) at++
    const raw = text.slice(start, at)
    if (raw === "true" || raw === "false") return { kind: "boolean", value: raw === "true" }
    if (raw === "null") return { kind: "null" }
    return { kind: "number", raw }
  }
  return value()
}

/**
 * The document as text. With `indent` it is laid out a level per line, as
 * `JSON.stringify(value, null, indent)` would; without, it is compact. A
 * number comes out as the digits it went in as.
 */
export function printJsonDoc(node: JsonNode, indent = 0, depth = 0): string {
  const line = indent ? `\n${" ".repeat(indent * (depth + 1))}` : ""
  const close = indent ? `\n${" ".repeat(indent * depth)}` : ""
  const colon = indent ? ": " : ":"
  switch (node.kind) {
    case "object":
      if (node.entries.length === 0) return "{}"
      return `{${node.entries
        .map(
          (entry) =>
            `${line}${JSON.stringify(entry.key)}${colon}${printJsonDoc(entry.value, indent, depth + 1)}`,
        )
        .join(",")}${close}}`
    case "array":
      if (node.items.length === 0) return "[]"
      return `[${node.items.map((item) => `${line}${printJsonDoc(item, indent, depth + 1)}`).join(",")}${close}]`
    case "string":
      return JSON.stringify(node.value)
    case "number":
      return node.raw
    case "boolean":
      return String(node.value)
    case "null":
      return "null"
  }
}

/** The node under a run of keys and positions, or `undefined` where the path leads nowhere. */
export function nodeAt(node: JsonNode, path: readonly (string | number)[]): JsonNode | undefined {
  let at: JsonNode | undefined = node
  for (const segment of path) {
    if (!at) return undefined
    if (at.kind === "object" && typeof segment === "string") {
      at = at.entries.find((entry) => entry.key === segment)?.value
    } else if (at.kind === "array" && typeof segment === "number") {
      at = at.items[segment]
    } else {
      return undefined
    }
  }
  return at
}

/** How many pairs or items a container holds, in its own word. */
export function nodeSize(node: JsonNode): string {
  if (node.kind === "object") {
    const n = node.entries.length
    return `${n.toLocaleString()} ${n === 1 ? "key" : "keys"}`
  }
  if (node.kind === "array") {
    const n = node.items.length
    return `${n.toLocaleString()} ${n === 1 ? "item" : "items"}`
  }
  return ""
}

/**
 * Whether a JSONPath names exactly one place: `$`, then only member names and
 * positions. A path with a wildcard, a filter, a slice or `..` matches a set
 * of places, and what is under one of its matches has no address to write to.
 */
export function definitePath(path: string): boolean {
  return /^\$(?:\.[A-Za-z_$][\w$]*|\[\d+\]|\["(?:[^"\\]|\\.)*"\])*$/.test(path.trim())
}
