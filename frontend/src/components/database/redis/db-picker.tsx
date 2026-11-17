"use client"

import { plural } from "@/lib/format"
import { ContainerPicker } from "@/components/database/kit/container-picker"
import type { RedisServer } from "@/components/database/redis/types"

/**
 * Which numbered database the page is about, with how many keys each holds.
 *
 * The list is the server's own: as many databases as it is configured for,
 * the ones that hold keys counted, the one the connection string names said
 * to be it. A cluster node and a sentinel have no numbered databases, and
 * draw no picker.
 *
 * Drawn by the database kit's `ContainerPicker`, the dropdown every other
 * workbench picks its schema or database with: a Redis database is that
 * engine's container, so it takes the same trigger, mark and menu, with a
 * database's keys as its figure. A choice stays on the page (`onPick`): the
 * address keeps it as `?db=`.
 */
export function DbPicker({
  server,
  db,
  onChange,
  fill = false,
  className,
}: {
  server: RedisServer | undefined
  /** The database on screen; unknown until the server has said which one the connection names. */
  db: number | undefined
  onChange: (db: number) => void
  /** The trigger takes the head's room, as it does in a rail. */
  fill?: boolean
  className?: string
}) {
  if (!server || db === undefined || !server.features.databases) return null
  const keys = new Map(server.keyspace.map((space) => [space.db, space.keys]))
  // A database past the server's count can still be what the address names;
  // it is listed so the control shows where the page is.
  const count = Math.max(server.databases, db + 1)
  const items = Array.from({ length: count }, (_, n) => ({
    name: `db${n}`,
    figure: `${keys.has(n) ? plural(keys.get(n)!, "key") : "empty"}${n === server.db ? " · connects here" : ""}`,
  }))
  return (
    <ContainerPicker
      items={items}
      current={`db${db}`}
      noun="database"
      nouns="databases"
      onPick={(name) => onChange(Number(name.slice(2)))}
      fill={fill}
      className={className}
    />
  )
}
