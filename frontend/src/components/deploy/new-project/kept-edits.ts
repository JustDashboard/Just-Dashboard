import type { ConfigureFlow } from "@/components/deploy/new-project/draft"

/**
 * What the person typed into the new-deployment form while the assistant was
 * working on the same draft.
 *
 * The form stays open while it works, and each of its saves is read back into the
 * page whole, so on its own a read-back silently put back whatever the person had
 * just typed. Each edit is kept here as the fields it touched, and every read-back
 * is laid under them: a field the person touched keeps the person's value, and
 * every other field takes the assistant's. The page saves them once the assistant
 * is done, and lets them go.
 *
 * A field is a leaf of the plan: a value, or a whole list (a list's rows have no
 * names to match an edit to one of them by), so touching one variable keeps the
 * person's list of them.
 */
export type KeptEdits = Record<string, { path: string[]; value: unknown }>

export const NO_KEPT_EDITS: KeptEdits = {}

/** The parts of the flow a person's edit can change; the rest is the server's. */
const EDITABLE = ["name", "profile", "configuration"] as const

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)

const sameValue = (a: unknown, b: unknown) =>
  Object.is(a, b) || JSON.stringify(a) === JSON.stringify(b)

function diff(a: unknown, b: unknown, path: string[], out: string[][]) {
  if (Object.is(a, b)) return
  if (isRecord(a) && isRecord(b)) {
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
      diff(a[key], b[key], [...path, key], out)
    }
    return
  }
  if (!sameValue(a, b)) out.push(path)
}

/** The fields one edit changed, from the flow it was made on to the one it made. */
export function editedPaths(before: ConfigureFlow, after: ConfigureFlow): string[][] {
  const out: string[][] = []
  for (const key of EDITABLE) diff(before[key], after[key], [key], out)
  return out
}

function valueAt(source: unknown, path: string[]): unknown {
  let at = source
  for (const key of path) {
    if (!isRecord(at)) return undefined
    at = at[key]
  }
  return at
}

function withValue<T>(target: T, path: string[], value: unknown): T {
  if (path.length === 0) return value as T
  const [key, ...rest] = path
  const base: Record<string, unknown> = isRecord(target) ? { ...target } : {}
  const next = withValue(base[key], rest, value)
  if (next === undefined) delete base[key]
  else base[key] = next
  return base as T
}

/** The edits kept so far, with those fields' values as the edit left them. */
export function keepEdits(kept: KeptEdits, paths: string[][], after: ConfigureFlow): KeptEdits {
  if (paths.length === 0) return kept
  const next = { ...kept }
  for (const path of paths) next[path.join("\u0000")] = { path, value: valueAt(after, path) }
  return next
}

/** A flow with the given fields of another laid over it. */
export function withPaths(
  flow: ConfigureFlow,
  paths: string[][],
  from: ConfigureFlow,
): ConfigureFlow {
  return paths.reduce((next, path) => withValue(next, path, valueAt(from, path)), flow)
}

/** A flow read back from the server, with every field the person touched put back. */
export function withKeptEdits(flow: ConfigureFlow, kept: KeptEdits): ConfigureFlow {
  return Object.values(kept).reduce((next, one) => withValue(next, one.path, one.value), flow)
}

export const hasKeptEdits = (kept: KeptEdits) => Object.keys(kept).length > 0
