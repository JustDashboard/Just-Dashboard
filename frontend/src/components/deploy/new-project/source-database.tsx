"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import type { DbDriverInfo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FlowPanel } from "@/components/flow"
import { DatabaseCreation } from "@/components/database/connect/start"
import { DatabaseReady, type CreatedDatabase } from "@/components/deploy/database-ready"
import { useSessionState } from "@/lib/view-state"

/** Database creation is the same flow as Databases, ending here with its address. */
export function SourceDatabase() {
  const [started, setStarted] = useSessionState<{ container: string; engine: string } | undefined>(
    "deploy.new.database.started",
    undefined,
  )
  const [engine, setEngine] = useState(started?.engine ?? "")
  const [created, setCreated] = useState<CreatedDatabase>()
  const catalogue = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
  )

  if (created) {
    return (
      <FlowPanel className="w-full min-w-0 p-4 xl:max-h-full xl:overflow-y-auto">
        <DatabaseReady
          created={created}
          onReset={() => {
            setCreated(undefined)
            setStarted(undefined)
            setEngine("")
          }}
        />
      </FlowPanel>
    )
  }

  return (
    <DatabaseCreation
      engine={engine}
      onChoose={(next) => setEngine(next ?? "")}
      drivers={catalogue.data}
      driversSettled={!catalogue.loading}
      resume={started}
      onStarted={setStarted}
      showAddress
      onReady={async (connection) => {
        const address = await get<{ url: string; reference?: string }>(
          `/databases/${connection.id}/url`,
          { target: "host" },
        )
        setCreated({ connection, ...address })
      }}
    />
  )
}
