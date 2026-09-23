/**
 * A unified diff of a whole working tree, cut into one diff per file.
 *
 * The terminal's Diff tab reads the work as git reports it — the unstaged
 * change in one request, the staged one in another — and draws it file by
 * file, so a reader can fold away the lockfile and read the component. The
 * cut is at each `diff --git a/… b/…` line, and a file is named by its new
 * path, which is the name the status list gives it too.
 */

export type FileDiff = {
  path: string
  body: string
  additions: number
  deletions: number
}

const HEADER = /^diff --git a\/(.+) b\/(.+)$/

export function splitDiff(body: string): FileDiff[] {
  const out: FileDiff[] = []
  let current: { path: string; lines: string[] } | null = null
  const close = () => {
    if (!current) return
    const text = current.lines.join("\n")
    out.push({ path: current.path, body: text, ...countChanges(text) })
  }
  for (const line of body.split("\n")) {
    const header = HEADER.exec(line)
    if (header) {
      close()
      current = { path: header[2], lines: [line] }
      continue
    }
    current?.lines.push(line)
  }
  close()
  return out
}

/** Lines added and removed, not counting the `+++`/`---` file headers. */
export function countChanges(body: string) {
  let additions = 0
  let deletions = 0
  let inHunk = false
  for (const line of body.split("\n")) {
    if (line.startsWith("@@")) inHunk = true
    else if (line.startsWith("diff --git ")) inHunk = false
    else if (inHunk && line.startsWith("+")) additions++
    else if (inHunk && line.startsWith("-")) deletions++
  }
  return { additions, deletions }
}
