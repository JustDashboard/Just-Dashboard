/**
 * GitHub-flavoured Markdown, read into a tree a component can draw.
 *
 * A pull request's description and every comment on it arrive as Markdown,
 * and printed verbatim they were "## What changed" and "**Why**" in a grey
 * paragraph: the reader parsed the syntax by eye before reading the words.
 * This reads the subset people actually write in a review — headings, lists
 * and task lists, fenced code, quotes, tables, rules, emphasis, inline code,
 * links, `#123` references and `@mentions` — into plain data, and
 * `components/git/markdown.tsx` draws it as React elements.
 *
 * **Nothing here is HTML.** The tree is drawn with elements and text nodes,
 * never `innerHTML`, so a comment cannot carry markup or script into the
 * page. Raw HTML in the source is dropped where it is layout (`<br>`,
 * `<details>`, an HTML comment a template left behind) and shown as the text
 * it is otherwise. Images become links to themselves: the page's `img-src`
 * is its own origin, and a comment should not be able to make the browser
 * fetch from anywhere it likes.
 */

export type Inline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "strong"; c: Inline[] }
  | { t: "em"; c: Inline[] }
  | { t: "del"; c: Inline[] }
  | { t: "link"; href: string; c: Inline[] }
  | { t: "ref"; n: number }
  | { t: "mention"; v: string }
  | { t: "br" }

export type Block =
  | { t: "heading"; level: number; c: Inline[] }
  | { t: "paragraph"; c: Inline[] }
  | { t: "code"; lang: string; v: string }
  | { t: "quote"; c: Block[] }
  | { t: "list"; ordered: boolean; start: number; items: ListItem[] }
  | {
      t: "table"
      align: ("left" | "center" | "right" | undefined)[]
      head: Inline[][]
      rows: Inline[][][]
    }
  | { t: "rule" }

export type ListItem = { task?: boolean; done?: boolean; c: Block[] }

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([\w+#.-]*)/
const HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/
const RULE = /^ {0,3}([-*_])(\s*\1){2,}\s*$/
const QUOTE = /^ {0,3}> ?/
const BULLET = /^( {0,3})([-*+])\s+(.*)$/
const ORDERED = /^( {0,3})(\d{1,9})[.)]\s+(.*)$/
const TABLE_RULE = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/

/** HTML a template leaves behind, dropped before anything is read. */
function stripHtml(source: string): string {
  return source
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(/<\/?(details|summary|p|div|sub|sup|kbd|picture|source|center)\b[^>]*>/gi, "")
    .replace(/<img\b[^>]*>/gi, "")
}

export function parseMarkdown(source: string): Block[] {
  const lines = stripHtml(source.replace(/\r\n?/g, "\n")).split("\n")
  return parseBlocks(lines)
}

function parseBlocks(lines: string[]): Block[] {
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (!line.trim()) {
      i++
      continue
    }

    const fence = FENCE.exec(line)
    if (fence) {
      const marker = fence[1]
      const body: string[] = []
      i++
      while (i < lines.length && !lines[i].trimStart().startsWith(marker)) body.push(lines[i++])
      i++
      blocks.push({ t: "code", lang: fence[2] ?? "", v: body.join("\n") })
      continue
    }

    const heading = HEADING.exec(line)
    if (heading) {
      blocks.push({ t: "heading", level: heading[1].length, c: parseInline(heading[2]) })
      i++
      continue
    }

    if (RULE.test(line)) {
      blocks.push({ t: "rule" })
      i++
      continue
    }

    if (QUOTE.test(line)) {
      const body: string[] = []
      while (i < lines.length && lines[i].trim() && QUOTE.test(lines[i])) {
        body.push(lines[i++].replace(QUOTE, ""))
      }
      blocks.push({ t: "quote", c: parseBlocks(body) })
      continue
    }

    if (BULLET.test(line) || ORDERED.test(line)) {
      const [list, next] = parseList(lines, i)
      blocks.push(list)
      i = next
      continue
    }

    if (line.includes("|") && i + 1 < lines.length && TABLE_RULE.test(lines[i + 1])) {
      const [table, next] = parseTable(lines, i)
      blocks.push(table)
      i = next
      continue
    }

    const body: string[] = []
    while (i < lines.length && lines[i].trim() && !startsBlock(lines[i])) body.push(lines[i++])
    if (body.length === 0) body.push(lines[i++])
    blocks.push({ t: "paragraph", c: parseInline(body.map((l) => l.trim()).join("\n")) })
  }
  return blocks
}

function startsBlock(line: string): boolean {
  return (
    FENCE.test(line) ||
    HEADING.test(line) ||
    RULE.test(line) ||
    QUOTE.test(line) ||
    BULLET.test(line) ||
    ORDERED.test(line)
  )
}

const indentOf = (line: string) => line.length - line.trimStart().length

/**
 * A run of items of one kind. An item owns every line after it that is
 * indented past its marker or blank-then-indented, and those lines are read
 * again as blocks once dedented — which is how a nested list or a code block
 * inside an item comes out right without a second grammar for them.
 */
function parseList(lines: string[], start: number): [Block, number] {
  const first = BULLET.exec(lines[start]) ?? ORDERED.exec(lines[start])!
  const ordered = !BULLET.test(lines[start])
  const base = first[1].length
  const items: ListItem[] = []
  let i = start
  while (i < lines.length) {
    const match = ordered ? ORDERED.exec(lines[i]) : BULLET.exec(lines[i])
    if (!match || match[1].length !== base) break
    const body = [match[3]]
    const inner = base + 2
    i++
    while (i < lines.length) {
      const line = lines[i]
      if (!line.trim()) {
        if (i + 1 < lines.length && lines[i + 1].trim() && indentOf(lines[i + 1]) >= inner) {
          body.push("")
          i++
          continue
        }
        break
      }
      if (indentOf(line) >= inner) {
        body.push(line.slice(Math.min(indentOf(line), inner + 2)))
        i++
        continue
      }
      // A line that is not a new item continues the item's paragraph.
      if (!startsBlock(line)) {
        body.push(line.trim())
        i++
        continue
      }
      break
    }
    const task = /^\[([ xX])\]\s+/.exec(body[0])
    if (task) body[0] = body[0].slice(task[0].length)
    items.push({
      task: Boolean(task) || undefined,
      done: task ? task[1] !== " " : undefined,
      c: parseBlocks(body),
    })
    // Blank lines between items of one list keep it one list.
    let peek = i
    while (peek < lines.length && !lines[peek].trim()) peek++
    const again = peek < lines.length && (ordered ? ORDERED : BULLET).exec(lines[peek])
    if (again && again[1].length === base) i = peek
    else break
  }
  return [{ t: "list", ordered, start: ordered ? Number(first[2]) : 1, items }, i]
}

function cells(row: string): string[] {
  return row
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split(/(?<!\\)\|/)
    .map((c) => c.trim().replace(/\\\|/g, "|"))
}

function parseTable(lines: string[], start: number): [Block, number] {
  const head = cells(lines[start])
  const align = cells(lines[start + 1]).map((c) =>
    c.startsWith(":") && c.endsWith(":")
      ? ("center" as const)
      : c.endsWith(":")
        ? ("right" as const)
        : c.startsWith(":")
          ? ("left" as const)
          : undefined,
  )
  const rows: Inline[][][] = []
  let i = start + 2
  while (i < lines.length && lines[i].trim() && lines[i].includes("|")) {
    const row = cells(lines[i++])
    rows.push(head.map((_, column) => parseInline(row[column] ?? "")))
  }
  return [{ t: "table", align, head: head.map((c) => parseInline(c)), rows }, i]
}

/** An address a link may point at: the web or mail, nothing that runs. */
export function safeHref(href: string): string | undefined {
  const value = href.trim()
  return /^(https?:\/\/|mailto:)/i.test(value) ? value : undefined
}

const DELIMITED: { open: string; t: "strong" | "em" | "del" }[] = [
  { open: "**", t: "strong" },
  { open: "__", t: "strong" },
  { open: "~~", t: "del" },
  { open: "*", t: "em" },
  { open: "_", t: "em" },
]

/**
 * One paragraph's text into runs. A scanner rather than a chain of regexes,
 * because the order things are recognised in is what makes `**a `b` c**`
 * bold around code rather than code inside a broken bold.
 */
export function parseInline(text: string): Inline[] {
  const out: Inline[] = []
  let buffer = ""
  const flush = () => {
    if (buffer) out.push({ t: "text", v: buffer })
    buffer = ""
  }
  let i = 0
  while (i < text.length) {
    const rest = text.slice(i)
    const ch = text[i]

    if (ch === "\\" && i + 1 < text.length && /[\\`*_{}[\]()#+\-.!|~<>]/.test(text[i + 1])) {
      buffer += text[i + 1]
      i += 2
      continue
    }
    if (ch === "\n") {
      flush()
      out.push({ t: "br" })
      i++
      continue
    }
    if (ch === "`") {
      const ticks = /^`+/.exec(rest)![0]
      const end = text.indexOf(ticks, i + ticks.length)
      if (end > 0) {
        flush()
        out.push({ t: "code", v: text.slice(i + ticks.length, end).trim() })
        i = end + ticks.length
        continue
      }
    }
    // An image is a link to itself, labelled by its alt text.
    // One level of parentheses inside the address, as GitHub allows: a
    // Wikipedia link ends in one.
    const link =
      /^(!?)\[((?:[^\]\\]|\\.)*)\]\(\s*<?((?:[^()\s>]|\([^()\s]*\))+)>?(?:\s+"[^"]*")?\s*\)/.exec(
        rest,
      )
    if (link) {
      const href = safeHref(link[3])
      flush()
      const label = link[2] || (link[1] ? "image" : link[3])
      if (href)
        out.push({ t: "link", href, c: link[1] ? [{ t: "text", v: label }] : parseInline(label) })
      else out.push(...parseInline(label))
      i += link[0].length
      continue
    }
    const auto =
      /^<(https?:\/\/[^>\s]+)>/.exec(rest) ?? /^(https?:\/\/[^\s<)]+[^\s<).,;:!?'"])/.exec(rest)
    if (auto && (i === 0 || /[\s(]/.test(text[i - 1]) || rest.startsWith("<"))) {
      flush()
      out.push({ t: "link", href: auto[1], c: [{ t: "text", v: auto[1] }] })
      i += auto[0].length
      continue
    }
    if (ch === "#" && (i === 0 || /[\s(]/.test(text[i - 1]))) {
      const ref = /^#(\d{1,7})\b/.exec(rest)
      if (ref) {
        flush()
        out.push({ t: "ref", n: Number(ref[1]) })
        i += ref[0].length
        continue
      }
    }
    if (ch === "@" && (i === 0 || /[\s(]/.test(text[i - 1]))) {
      const mention = /^@([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))(?:\/[\w.-]+)?\b/.exec(rest)
      if (mention) {
        flush()
        out.push({ t: "mention", v: mention[0].slice(1) })
        i += mention[0].length
        continue
      }
    }
    const delimiter = DELIMITED.find((d) => rest.startsWith(d.open))
    if (delimiter) {
      const { open } = delimiter
      // Underscores inside a word are part of it: snake_case is not emphasis.
      const wordy = open[0] === "_" && i > 0 && /\w/.test(text[i - 1])
      const end = wordy ? -1 : closing(text, i + open.length, open)
      if (end > i + open.length) {
        flush()
        out.push({ t: delimiter.t, c: parseInline(text.slice(i + open.length, end)) })
        i = end + open.length
        continue
      }
    }
    buffer += ch
    i++
  }
  flush()
  return out
}

/** Where a run opened by `open` at `from` closes, skipping code spans; -1 if it never does. */
function closing(text: string, from: number, open: string): number {
  if (/\s/.test(text[from] ?? " ")) return -1
  let i = from
  while (i < text.length) {
    if (text[i] === "`") {
      const end = text.indexOf("`", i + 1)
      if (end < 0) return -1
      i = end + 1
      continue
    }
    if (text.startsWith(open, i) && !/\s/.test(text[i - 1])) {
      // A single marker must not close on the first half of a double one.
      if (open.length === 1 && text[i + 1] === open) {
        i += 2
        continue
      }
      if (open[0] === "_" && /\w/.test(text[i + open.length] ?? "")) {
        i++
        continue
      }
      return i
    }
    i++
  }
  return -1
}

/** The words of a Markdown body with the syntax taken out, for a one-line excerpt. */
export function markdownText(source: string): string {
  const words: string[] = []
  const walkInline = (runs: Inline[]) => {
    for (const run of runs) {
      if (run.t === "text" || run.t === "code") words.push(run.v)
      else if (run.t === "ref") words.push(`#${run.n}`)
      else if (run.t === "mention") words.push(`@${run.v}`)
      else if (run.t === "br") words.push(" ")
      else if ("c" in run) walkInline(run.c)
    }
  }
  const walk = (blocks: Block[]) => {
    for (const block of blocks) {
      if (block.t === "heading" || block.t === "paragraph") walkInline(block.c)
      else if (block.t === "quote") walk(block.c)
      else if (block.t === "list") block.items.forEach((item) => walk(item.c))
      words.push(" ")
    }
  }
  walk(parseMarkdown(source))
  return words.join("").replace(/\s+/g, " ").trim()
}
