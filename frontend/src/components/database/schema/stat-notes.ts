/**
 * What the engine could not say about a table, as quiet lines.
 *
 * The statistics routes explain a missing figure in a note, and the note
 * carries the driver's own refusal in brackets — error number, account, host —
 * which is the server's log line, not the reader's sentence. Here the bracket
 * is taken out, and a line two routes both sent is said once.
 */
export function quietNotes(...lists: (readonly string[] | undefined)[]): string[] {
  const seen = new Set<string>()
  const lines: string[] = []
  for (const note of lists.flatMap((list) => list ?? [])) {
    const line = withoutDriverError(note).trim()
    if (line === "" || seen.has(line)) continue
    seen.add(line)
    lines.push(line)
  }
  return lines
}

/** A bracket that opens on the driver's error is the driver's: `(Error 1142 (42000): …)`. */
const DRIVER_ERROR = /\s*\((?:Error|ERROR|error|pq:|mssql:|ORA-|SQLSTATE|code:)/

function withoutDriverError(note: string): string {
  const found = DRIVER_ERROR.exec(note)
  if (!found) return note
  const open = note.indexOf("(", found.index)
  let depth = 0
  for (let at = open; at < note.length; at += 1) {
    if (note[at] === "(") depth += 1
    else if (note[at] === ")") {
      depth -= 1
      if (depth === 0) return note.slice(0, found.index) + note.slice(at + 1)
    }
  }
  // A bracket that never closes: everything from it on was the error.
  return note.slice(0, found.index)
}
