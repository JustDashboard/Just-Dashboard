import { bytesId, bytesLabel } from "@/components/database/redis/bytes"
import type {
  RedisKey,
  RedisPage,
  RedisTree,
  RedisTreeFolder,
} from "@/components/database/redis/types"

/**
 * What a scan has found so far, folded together page by page.
 *
 * The server never lists a keyspace whole: `SCAN` hands over a slice and a
 * cursor, and may hand the same key over twice. So a listing is held as the
 * keys seen so far, each once, and a namespace's count is the sum of what
 * each call examined — "about", until the walk is complete.
 */

export type KeyList = {
  keys: RedisKey[]
  /** Every key in the database, whatever the pattern. */
  total: number
  db: number
}

/** A page of keys added to the ones already held, each key once. */
export function mergeKeys(held: KeyList | undefined, page: RedisPage): KeyList {
  const seen = new Set(held?.keys.map((entry) => bytesId(entry.key)))
  const keys = held ? [...held.keys] : []
  for (const entry of page.keys) {
    const id = bytesId(entry.key)
    if (seen.has(id)) continue
    seen.add(id)
    keys.push(entry)
  }
  return { keys, total: page.total, db: page.db }
}

export type TreeLevel = {
  db: number
  /** Namespaces directly under this one, largest first. */
  folders: RedisTreeFolder[]
  /** Namespaces the server left out past its cap. */
  foldersOmitted: number
  /** Keys directly under it: the first of them, by name. */
  keys: RedisKey[]
  /** How many keys sit directly under it. */
  keyCount: number
  /** Keys under it, counted by the calls so far. */
  count: number
  types: Record<string, number>
  /** Keys the server has walked for this level. */
  scanned: number
  total: number
  complete: boolean
}

function addCounts(into: Record<string, number>, from: Record<string, number>) {
  const out = { ...into }
  for (const [type, n] of Object.entries(from)) out[type] = (out[type] ?? 0) + n
  return out
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true })

/**
 * One more call of the namespace walk laid over the ones before it. Each call
 * counts only what it examined, so the pieces are added by namespace; the
 * order — largest first, then by name — is restored over the whole.
 */
export function mergeLevel(held: TreeLevel | undefined, page: RedisTree): TreeLevel {
  const folders = new Map<string, RedisTreeFolder>(
    held?.folders.map((folder) => [bytesId(folder.prefix), folder]),
  )
  for (const folder of page.folders) {
    const id = bytesId(folder.prefix)
    const before = folders.get(id)
    folders.set(
      id,
      before
        ? {
            ...before,
            count: before.count + folder.count,
            keyCount: before.keyCount + folder.keyCount,
            folders: before.folders + folder.folders,
            types: addCounts(before.types, folder.types),
          }
        : folder,
    )
  }
  const keys = new Map<string, RedisKey>(held?.keys.map((entry) => [bytesId(entry.key), entry]))
  for (const entry of page.keys) keys.set(bytesId(entry.key), entry)
  return {
    db: page.db,
    folders: [...folders.values()].sort(
      (a, b) => b.count - a.count || byName(bytesLabel(a.name), bytesLabel(b.name)),
    ),
    foldersOmitted: (held?.foldersOmitted ?? 0) + (page.foldersOmitted ?? 0),
    keys: [...keys.values()].sort((a, b) => byName(bytesLabel(a.key), bytesLabel(b.key))),
    keyCount: (held?.keyCount ?? 0) + page.keyCount,
    count: (held?.count ?? 0) + page.count,
    types: addCounts(held?.types ?? {}, page.types),
    scanned: (held?.scanned ?? 0) + page.scanned,
    total: page.total,
    complete: page.complete,
  }
}

/** A key's name as a tree shows it under its namespace: what follows the prefix. */
export function leafName(key: RedisKey["key"], prefix: RedisKey["key"]): string {
  const name = bytesLabel(key)
  const head = bytesLabel(prefix)
  return head && name.startsWith(head) ? name.slice(head.length) : name
}

/**
 * How far a scan has come, in the words the foot of the list says them.
 * `scanned` is absent for a flat listing, where the server says only whether
 * the cursor has come round. Under a pattern or a type it is not used
 * either: the server then hands over only the keys that match, so how many
 * it handed over says how many were found, not how many it looked at.
 */
export function scanProgress(input: {
  found: number
  scanned?: number
  total: number
  complete: boolean
  filtered: boolean
}): string {
  const { found, scanned, total, complete, filtered } = input
  const all = total.toLocaleString()
  if (scanned !== undefined && !filtered) {
    return complete
      ? `Scanned all ${all} keys`
      : `Scanned ${Math.min(scanned, total).toLocaleString()} of about ${all}`
  }
  if (complete) {
    return filtered
      ? `${found.toLocaleString()} of ${all} keys match`
      : `All ${found.toLocaleString()} keys`
  }
  return filtered
    ? `${found.toLocaleString()} found so far in ${all} keys`
    : `${found.toLocaleString()} of about ${all} keys`
}
