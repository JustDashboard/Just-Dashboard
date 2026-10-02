import type { FileEntry, FileFindHit } from "@/lib/types"

export type FileSearchHit = Pick<FileFindHit, "path" | "name" | "isDir"> & {
  size?: number
  modified?: string
  matches?: number[]
  line?: number
  snippet?: string
  ranges?: [number, number][]
}

/** Match the directory already in hand before the disk walk answers. */
export function localFileMatches(entries: FileEntry[], query: string): FileSearchHit[] {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  return entries
    .flatMap((entry) => {
      const name = entry.name.toLocaleLowerCase()
      const matches = new Set<number>()
      let score = 0
      for (const term of terms) {
        let position = 0
        for (const char of term) {
          const index = name.indexOf(char, position)
          if (index < 0) return []
          matches.add(index)
          score += index === position ? 3 : 1
          position = index + char.length
        }
        if (name.startsWith(term)) score += 20
        if (name === term) score += 40
      }
      return [{ hit: { ...entry, matches: [...matches].sort((a, b) => a - b) }, score }]
    })
    .sort((a, b) => b.score - a.score || a.hit.name.localeCompare(b.hit.name))
    .slice(0, terms.length ? 60 : 12)
    .map(({ hit }) => hit)
}

export function mergeFileMatches(local: FileSearchHit[], remote: FileSearchHit[]) {
  const paths = new Set(remote.map((hit) => hit.path))
  return [...remote, ...local.filter((hit) => !paths.has(hit.path))].slice(0, 60)
}

export function editorHref(path: string, root?: string, line?: number) {
  const params = new URLSearchParams({ path })
  if (root) params.set("root", root)
  if (line) params.set("line", String(line))
  return `/files/editor?${params}`
}
