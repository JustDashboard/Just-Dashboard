"use client"

import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { RedisServer } from "@/components/database/redis/types"

/**
 * Which numbered database the page is about, with how many keys each holds.
 *
 * The list is the server's own: as many databases as it is configured for,
 * the ones that hold keys counted, the one the connection string names said
 * to be it. A cluster node and a sentinel have no numbered databases, and
 * draw no picker.
 */
export function DbPicker({
  server,
  db,
  onChange,
  className,
}: {
  server: RedisServer | undefined
  /** The database on screen; unknown until the server has said which one the connection names. */
  db: number | undefined
  onChange: (db: number) => void
  className?: string
}) {
  if (!server || db === undefined || !server.features.databases) return null
  const keys = new Map(server.keyspace.map((space) => [space.db, space.keys]))
  // A database past the server's count can still be what the address names;
  // it is listed so the control shows where the page is.
  const count = Math.max(server.databases, db + 1)
  return (
    <Select value={String(db)} onValueChange={(value) => onChange(Number(value))}>
      <SelectTrigger
        size="sm"
        aria-label="Logical database"
        className={cn(
          "gap-1.5 px-2 font-mono text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7",
          className,
        )}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent position="popper" align="start" className="max-h-80">
        {Array.from({ length: count }, (_, n) => (
          <SelectItem
            key={n}
            value={String(n)}
            className="font-mono text-xs"
            hint={
              <span className="numeric font-sans">
                {keys.has(n) ? plural(keys.get(n)!, "key") : "empty"}
                {n === server.db ? " · connects here" : ""}
              </span>
            }
          >
            db{n}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
