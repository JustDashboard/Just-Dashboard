"use client"

import { useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import { mongoCollections, mongoDatabases, type MongoTarget } from "@/components/database/mongo/api"
import { useDatabase, useDatabaseRole } from "@/components/database/shell/database-context"

/**
 * What every MongoDB page starts from: which database and collection the
 * address names, and what the reader's role may do.
 *
 * The database is the address's (`?db=`), else the one the connection string
 * names. It is always sent by name once known, so a page never shows one
 * database's collections over another's documents.
 *
 * The capabilities are affordances — the routes decide — and each matches
 * what its routes ask for: a write is `service.control`, a removal
 * `destructive`, the profiler and the accounts `system.admin`. A protected
 * connection draws no write control at all.
 */
export function useMongo() {
  const ctx = useDatabase()
  const role = useDatabaseRole()
  const { id, selection, conn } = ctx

  const database = selection.db || conn.database || ""
  const collection = selection.collection
  const target = useMemo<MongoTarget>(
    () => ({ id, database, collection }),
    [id, database, collection],
  )

  return {
    ...ctx,
    /** The database on screen: the named one, else the connection string's. `""` when neither says. */
    database,
    /** The collection the address names, `""` when none is open. */
    collection,
    target,
    // The console's own route and a pipeline need `service.control`, as every run does.
    ...role,
  }
}

export type Mongo = ReturnType<typeof useMongo>

/**
 * The two lists the rail is drawn from, read once for the page: the server's
 * databases with their sizes, and the open database's collections with their
 * counts. Both are slow lists — one command per database, one per collection
 * — so they are read on arrival and after a write here, and otherwise once a
 * minute.
 */
export function useCatalog(mongo: Mongo) {
  const { id, database } = mongo
  const databases = usePoll((signal) => mongoDatabases(id, signal), 120_000, [id])
  const collections = usePoll((signal) => mongoCollections(id, database, signal), 60_000, [
    id,
    database,
  ])
  const current = collections.data?.collections.find((entry) => entry.name === mongo.collection)
  return {
    databases,
    collections,
    /** The open collection as the list knows it; `undefined` while unread or when it is not there. */
    current,
    refresh: () => {
      collections.refresh()
      databases.refresh()
    },
  }
}

export type Catalog = ReturnType<typeof useCatalog>
