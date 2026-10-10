"use client"

import { useCallback, useMemo } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { redisServer, type RedisTarget } from "@/components/database/redis/api"
import { useDatabase } from "@/components/database/shell/database-context"

/** A logical database as an address writes it: a small whole number. */
const DATABASE = /^\d{1,5}$/

/**
 * What every Redis page starts from: the server as it describes itself, the
 * logical database the page is about, and what the reader's role may do.
 *
 * The database comes from the address (`?db=`). An address that names none
 * means the one the connection string names — which the server reports, and
 * which is not database 0: the old browser showed `db0` selected over the
 * keys of whichever database the connection was made to. So nothing is sent
 * as `db` until one is named, and what the picker shows is the server's own
 * answer.
 *
 * The three capabilities are affordances — the routes decide — and each
 * matches what its routes ask for: a write is `service.control`, a removal
 * `destructive`, the live feeds and the server's own settings
 * `system.admin`. A protected connection draws none of the first two.
 */
export function useRedis() {
  const ctx = useDatabase()
  const { can } = useAuth()
  const { id, selection, select } = ctx

  const server = usePoll((signal) => redisServer(id, signal), 15_000, [id])

  const named = DATABASE.test(selection.db) ? Number(selection.db) : undefined
  const target = useMemo<RedisTarget>(() => ({ id, db: named }), [id, named])
  const db = named ?? server.data?.db
  // Which database a listing held on the page is a listing *of*. The address
  // may not name one — then it is the connection's own, which the server
  // says a moment later — and the first key opened from a list puts the
  // number in the address. Both are the same database, and a list keyed by
  // what the address says would be thrown away and scanned again for it. So
  // lists are keyed by the database itself, and wait the moment it takes to
  // learn which that is; a server that will not say is asked for its keys
  // anyway, so the failure is the list's to show.
  const scope = db !== undefined ? String(db) : server.error ? "own" : undefined

  const setDb = useCallback(
    // Another database holds other keys: the one that was open is not in it.
    (next: number) => select({ db: String(next), key: null, keyB64: null }),
    [select],
  )

  return {
    ...ctx,
    server,
    /** What requests are made about: the database only where the address names one. */
    target,
    /** The database on screen: the named one, else the connection string's. */
    db,
    /** What a held listing is keyed by; `undefined` until the database is known. */
    scope,
    setDb,
    /**
     * The console's own route: any command needs `service.control`, and a
     * protected connection still runs the ones that only read.
     */
    canRun: can("service.control"),
    canWrite: !ctx.readOnly && can("service.control"),
    canDestroy: !ctx.readOnly && can("destructive"),
    /**
     * Disconnecting a client is a removal the server asks the destructive
     * capability for, and lets through on a protected connection: it stops
     * work and changes no data.
     */
    canKill: can("destructive"),
    admin: can("system.admin"),
  }
}

export type Redis = ReturnType<typeof useRedis>
