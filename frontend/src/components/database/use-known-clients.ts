"use client"

import { useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import type { DbTopology } from "@/lib/types"
import { knownClients } from "@/components/database/clients"
import { read } from "@/components/database/home/read"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The deployments and containers the consumers route says use this database,
 * by name, so a session that names itself after one is drawn as it. Read at
 * the home picture's pace; a read that fails leaves every session drawn from
 * its own name, which is what it would be without the route.
 */
export function useKnownClients() {
  const { id } = useDatabase()
  const topology = usePoll(
    (signal) =>
      read<DbTopology>(
        `/databases/${id}/consumers`,
        (answer) => Array.isArray(answer.nodes) && Array.isArray(answer.edges),
        undefined,
        signal,
      ),
    45_000,
    [id],
  )
  return useMemo(() => knownClients(topology.data, id), [topology.data, id])
}
