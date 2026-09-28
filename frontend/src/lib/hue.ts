/**
 * A stable colour for a name — an author, a service, a process — picked from
 * the `--tag-*` hues, which all sit at one lightness so no name reads louder
 * than another.
 *
 * It was written three times: the git author mark, the build transcript's
 * lanes and the log console's processes, each with its own copy of the hash
 * and its own idea of which hues were allowed. One copy, two palettes.
 */

/** Every tag hue: for a mark that stands alone, like a commit's author. */
export const HUES = [
  "var(--tag-blue)",
  "var(--tag-green)",
  "var(--tag-amber)",
  "var(--tag-violet)",
  "var(--tag-red)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-slate)",
] as const

/**
 * The hues that cannot be mistaken for a state: no red, no amber. For a name
 * drawn beside readings that carry the status hues — a line of a log, a step
 * of a build — where a process in red would read as a process that failed.
 */
export const LANES = [
  "var(--tag-blue)",
  "var(--tag-green)",
  "var(--tag-violet)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-slate)",
] as const

/** Stable per name, so the same name keeps its colour across the page and across visits. */
export function hueFor(name: string, palette: readonly string[] = HUES): string {
  let hash = 0
  for (let i = 0; i < name.length; i++) hash = (hash * 31 + name.charCodeAt(i)) >>> 0
  return palette[hash % palette.length]
}
