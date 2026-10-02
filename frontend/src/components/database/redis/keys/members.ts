import { redisMembers, type MembersQuery, type RedisTarget } from "@/components/database/redis/api"
import { bytesId } from "@/components/database/redis/bytes"
import type { RedisBytes, RedisMembers, RedisRow } from "@/components/database/redis/types"
import { usePaged } from "@/components/database/redis/use-paged"

/** How many members one page asks for. */
export const MEMBER_PAGE = 200

export type MemberRows = {
  rows: RedisRow[]
  /** The key's real cardinality, which is not how many rows are loaded. */
  length: number
}

/**
 * What tells one member of a key from another, by the kind of key: a hash's
 * rows are its fields, a set's and a sorted set's their members (two members
 * may share a score, so the score is no identity), a list's their positions,
 * a stream's their entry ids.
 */
export function memberId(type: string, row: RedisRow): string {
  if (type === "hash") return bytesId(row.field ?? "")
  if (type === "list") return `i:${row.index}`
  if (type === "stream") return `e:${row.id}`
  return bytesId(row.value ?? "")
}

/**
 * A page of members added to the ones held. A scan may hand the same field
 * or member over on two pages; the later reading replaces the earlier in
 * place, so a row is drawn once and holds its newest value.
 */
export function mergeMembers(
  held: MemberRows | undefined,
  page: Pick<RedisMembers, "rows" | "length" | "type">,
): MemberRows {
  const rows = held ? [...held.rows] : []
  const at = new Map(rows.map((row, index) => [memberId(page.type, row), index]))
  for (const row of page.rows) {
    const id = memberId(page.type, row)
    const seen = at.get(id)
    if (seen === undefined) {
      at.set(id, rows.length)
      rows.push(row)
    } else {
      rows[seen] = row
    }
  }
  return { rows, length: page.length }
}

/** The members of one key, a page at a time, read again whenever `epoch` rises. */
export function useMembers(
  target: RedisTarget,
  name: RedisBytes,
  query: MembersQuery,
  epoch: number,
) {
  return usePaged(
    {
      fetch: (cursor, signal) =>
        redisMembers(target, name, { count: MEMBER_PAGE, ...query, cursor }, signal),
      merge: mergeMembers,
      next: (page) => (page.done ? null : page.cursor),
    },
    JSON.stringify([target.id, target.db ?? null, bytesId(name), query]),
    { epoch },
  )
}
