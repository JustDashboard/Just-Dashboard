"use client"

import {
  backupReadings,
  feedsByConnection,
  fleetConcerns,
  sortFleet,
  storedBytes,
} from "@/components/database/fleet/fleet"
import {
  readBackupSummary,
  readFleet,
  readInventory,
  readTopology,
} from "@/components/database/fleet/read"
import type { DbInventory } from "@/components/database/fleet/types"
import { useDatabases } from "@/components/database/shell/databases-context"
import { usePoll } from "@/hooks/use-poll"
import { post } from "@/lib/api"
import type { DbFleetEntry } from "@/lib/types"
import { useCallback, useMemo, useState } from "react"

/** How often the fleet is read, and how often while something is not answering. */
const STEADY_MS = 30_000
const WATCHING_MS = 5_000

/**
 * Everything the control center reads, in one place: the fleet, how well each
 * database is backed up, what the map says each one feeds, and the readings
 * and concerns worked out from the three.
 *
 * The fleet is the page: the other two reads decorate it, so only its absence
 * is the page's loading or error state, and a failed poll of any of them
 * leaves what is drawn where it is. It is read every thirty seconds, and
 * every five while a database is down or being started — the reader is
 * waiting for that one to change.
 *
 * `measured` is what discovery weighed on disk for the connections that are
 * files, by connection id: the engine reports no size for those, and the one
 * figure every part of the page says for "stored" is worked out here.
 */
export function useFleet(measured?: ReadonlyMap<number, number>) {
  const { engineFor } = useDatabases()
  const [interval, setIntervalMs] = useState(STEADY_MS)
  const fleet = usePoll(readFleet, interval)

  const watching = Boolean(
    fleet.data?.connections.some((entry) => !entry.ok && entry.state !== "broken"),
  )
  const wanted = watching ? WATCHING_MS : STEADY_MS
  if (wanted !== interval) setIntervalMs(wanted)

  const [dumpEvery, setDumpEvery] = useState(60_000)
  const summary = usePoll(readBackupSummary, dumpEvery)
  // A dump in flight is read often enough to see it end.
  const dumping = Boolean(summary.data?.connections.some((row) => row.job?.status === "running"))
  const dumpWanted = dumping ? 3_000 : 60_000
  if (dumpWanted !== dumpEvery) setDumpEvery(dumpWanted)

  const topology = usePoll(readTopology, 45_000)

  const backups = useMemo(
    () => backupReadings(fleet.data, summary.data),
    [fleet.data, summary.data],
  )
  const feeds = useMemo(() => feedsByConnection(topology.data), [topology.data])
  const dumps = useCallback((entry: DbFleetEntry) => engineFor(entry).can("dump"), [engineFor])
  const concernsOf = useCallback(
    (entry: DbFleetEntry) =>
      fleetConcerns(entry, { backup: backups.get(entry.id), dumps: dumps(entry) }),
    [backups, dumps],
  )
  const entries = useMemo(
    () => sortFleet(fleet.data?.connections ?? [], concernsOf),
    [fleet.data, concernsOf],
  )
  const storedOf = useCallback((entry: DbFleetEntry) => storedBytes(entry, measured), [measured])

  return {
    fleet,
    summary,
    topology,
    entries,
    backups,
    feeds,
    dumps,
    storedOf,
    concernsOf,
  }
}

export type FleetData = ReturnType<typeof useFleet>

/**
 * What discovery found on this machine. Only an administrator can connect or
 * ignore any of it, and only an administrator is told the whole of it, so the
 * list is read for nobody else.
 *
 * `scan` runs the slow file scan now and waits for it; the reading it builds
 * is what the next read is answered from.
 */
export function useInventory(enabled: boolean) {
  const inventory = usePoll(readInventory, 30_000, [], { enabled })
  const [scanning, setScanning] = useState(false)
  const [scanError, setScanError] = useState<Error>()
  const refresh = inventory.refresh
  const scan = useCallback(async () => {
    setScanning(true)
    setScanError(undefined)
    try {
      await post<DbInventory>("/databases/inventory/scan", {})
      refresh()
    } catch (err) {
      setScanError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setScanning(false)
    }
  }, [refresh])
  return { ...inventory, scan, scanning, scanError }
}

export type InventoryData = ReturnType<typeof useInventory>
