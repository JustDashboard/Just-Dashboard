import type { FileEntry, FileFindHit, FilePlaces } from "@/lib/types"
import { baseOf, isWithin, parentOf } from "./media"

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

/**
 * The home a folder belongs to: the account home it is inside, else the one
 * person's home on a one-account server, else where the dashboard starts.
 */
export function homeFor(
  folder: string,
  places: Pick<FilePlaces, "home" | "places"> | undefined,
): string | undefined {
  const homes = (places?.places ?? []).filter((p) => p.kind === "home" || p.kind === "user")
  const users = homes.filter((p) => p.kind === "user")
  return (
    homes
      .map((p) => p.path)
      .filter((path) => path !== "/" && isWithin(folder, path))
      .sort((a, b) => b.length - a.length)[0] ?? (users.length === 1 ? users[0].path : places?.home)
  )
}

export type SearchScope = { key: "folder" | "home" | "everywhere"; label: string; path: string }

/**
 * Where a search can start: the folder on screen, the home it is in, and the
 * whole of what the dashboard may read.
 *
 * "From home" used to mean the dashboard's own home — `/root`, on the usual
 * install — so pressing it while browsing `/home/ubuntu` searched a sibling of
 * that folder rather than anything around it, and the button read as broken.
 * Home is now `homeFor` the folder — the same home the strip's house button
 * goes to. Two scopes that are the same folder are one choice.
 */
export function searchScopes(
  folder: string,
  places: Pick<FilePlaces, "home" | "roots" | "places"> | undefined,
): SearchScope[] {
  const home = homeFor(folder, places)
  const scopes: SearchScope[] = [{ key: "folder", label: "This folder", path: folder }]
  if (home) scopes.push({ key: "home", label: "Home", path: home })
  scopes.push({ key: "everywhere", label: "Everywhere", path: places?.roots[0] ?? "/" })
  return scopes.filter((scope, i) => scopes.findIndex((s) => s.path === scope.path) === i)
}

/**
 * Where a hit lives, said from the scope that found it: `ubuntu/Downloads`
 * rather than the hit's own path, whose last word is the name printed right
 * above it.
 */
export function hitLocation(path: string, scope: string): string {
  const parent = parentOf(path)
  if (scope === "/" || !isWithin(parent, scope)) return parent
  return baseOf(scope) + parent.slice(scope.length)
}
