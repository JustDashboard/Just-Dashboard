import { bytesId } from "@/components/database/redis/bytes"
import type { RedisBytes } from "@/components/database/redis/types"

/**
 * When this page last read each key.
 *
 * The server keeps one clock per key — how long since anything read or wrote
 * it — and the dashboard's own reads reset it: listing a key reads its size,
 * and so does opening it. So "idle for 4s" on a key the list drew four
 * seconds ago is the dashboard looking at itself. Every read of a key that
 * goes through here is noted, and a key's idle time is shown only when it is
 * longer than this page could have caused.
 */
const reads = new Map<string, number>()
/** Past this many keys the oldest notes are dropped: a tab left open all day does not keep every name it listed. */
const CAP = 20_000

const slot = (id: number, db: number, key: RedisBytes) => `${id}:${db}:${bytesId(key)}`

/** `db` is the database the server says it read, never the one the address may have left unnamed. */
export function noteRead(id: number, db: number, key: RedisBytes, at = Date.now()) {
  if (reads.size >= CAP) {
    let drop = CAP / 4
    for (const name of reads.keys()) {
      reads.delete(name)
      if (--drop <= 0) break
    }
  }
  const name = slot(id, db, key)
  // Re-inserted, so the notes stay in the order the keys were last read.
  reads.delete(name)
  reads.set(name, at)
}

/** When this page last read the key, or `undefined` if it has not. */
export function lastRead(id: number, db: number, key: RedisBytes): number | undefined {
  return reads.get(slot(id, db, key))
}

/** The clock between this page's read and the server's count of seconds, with the round trip in it. */
const SLACK_MS = 2000

/**
 * Whether an idle time is this page's own doing: the server says the key has
 * been idle no longer than the time since this page last read it. A key read
 * by something else since reads shorter than that, and is the truth.
 */
export function ownEcho(
  idleSeconds: number,
  readAt: number | undefined,
  now = Date.now(),
): boolean {
  if (readAt === undefined) return false
  const since = now - readAt
  return Math.abs(idleSeconds * 1000 - since) <= SLACK_MS
}
