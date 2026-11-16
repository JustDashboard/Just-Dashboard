/**
 * A unified diff of two texts, for reviewing an edit before it is written.
 *
 * The editor is most often pointed at a file that keeps a server up — an nginx
 * vhost, a systemd unit, a compose file — and the question before Save is
 * "what exactly did I change", which a scrollbar and a memory cannot answer.
 * This is the same shape `git diff` prints, so `DiffView` draws it with the
 * colouring the git page already uses.
 *
 * The algorithm is the plain one: the common head and tail are peeled off
 * first, because an edit to a config file is usually a few lines in the
 * middle of a few hundred unchanged ones, and the remainder is aligned by
 * longest common subsequence. That table is quadratic, so it is capped: a
 * rewrite too large to align in a few million cells reports `null`, and the
 * editor says so rather than freezing the tab to draw a diff nobody could
 * read anyway.
 */
const MAX_CELLS = 4_000_000
const CONTEXT = 3

type Op = { kind: " " | "-" | "+"; text: string }

export function unifiedDiff(before: string, after: string, name: string): string | null {
  const a = splitLines(before)
  const b = splitLines(after)

  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++
  }

  const midA = a.slice(head, a.length - tail)
  const midB = b.slice(head, b.length - tail)
  if ((midA.length + 1) * (midB.length + 1) > MAX_CELLS) return null

  const ops: Op[] = []
  for (let i = 0; i < head; i++) ops.push({ kind: " ", text: a[i] })
  for (const op of align(midA, midB)) ops.push(op)
  for (let i = a.length - tail; i < a.length; i++) ops.push({ kind: " ", text: a[i] })

  return render(ops, name)
}

function splitLines(text: string): string[] {
  if (text === "") return []
  const lines = text.split("\n")
  // A trailing newline is the end of the last line, not an extra empty one.
  if (lines[lines.length - 1] === "") lines.pop()
  return lines
}

/** Longest common subsequence over the changed middle, as edit operations. */
function align(a: string[], b: string[]): Op[] {
  const rows = a.length + 1
  const cols = b.length + 1
  const table = new Uint32Array(rows * cols)
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i * cols + j] =
        a[i] === b[j]
          ? table[(i + 1) * cols + j + 1] + 1
          : Math.max(table[(i + 1) * cols + j], table[i * cols + j + 1])
    }
  }
  const ops: Op[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      ops.push({ kind: " ", text: a[i] })
      i++
      j++
    } else if (table[(i + 1) * cols + j] >= table[i * cols + j + 1]) {
      ops.push({ kind: "-", text: a[i] })
      i++
    } else {
      ops.push({ kind: "+", text: b[j] })
      j++
    }
  }
  while (i < a.length) ops.push({ kind: "-", text: a[i++] })
  while (j < b.length) ops.push({ kind: "+", text: b[j++] })
  return ops
}

/** Hunks with three lines of context, in the format `git diff` prints. */
function render(ops: Op[], name: string): string {
  const out: string[] = [`--- a/${name}`, `+++ b/${name}`]
  let i = 0
  let oldLine = 1
  let newLine = 1
  let changed = false
  while (i < ops.length) {
    if (ops[i].kind === " ") {
      oldLine++
      newLine++
      i++
      continue
    }
    // A hunk starts CONTEXT lines before the first change and runs until the
    // gap after a change exceeds twice the context, where the next one starts.
    const start = Math.max(0, i - CONTEXT)
    let end = i
    let last = i
    while (end < ops.length) {
      if (ops[end].kind !== " ") last = end
      else if (end - last > CONTEXT * 2) break
      end++
    }
    end = Math.min(ops.length, last + CONTEXT + 1)

    let oldStart = oldLine
    let newStart = newLine
    for (let k = i - 1; k >= start; k--) {
      oldStart--
      newStart--
    }
    let oldCount = 0
    let newCount = 0
    const body: string[] = []
    for (let k = start; k < end; k++) {
      const op = ops[k]
      body.push(op.kind + op.text)
      if (op.kind !== "+") oldCount++
      if (op.kind !== "-") newCount++
    }
    out.push(`@@ -${oldStart},${oldCount} +${newStart},${newCount} @@`, ...body)
    changed = true

    for (let k = i; k < end; k++) {
      if (ops[k].kind !== "+") oldLine++
      if (ops[k].kind !== "-") newLine++
    }
    i = end
  }
  return changed ? out.join("\n") : ""
}
