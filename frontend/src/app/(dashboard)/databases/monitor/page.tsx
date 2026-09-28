"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { MonitorTab } from "@/components/database/monitor-tab"
import { useDatabase } from "@/components/database/db-context"

export default function MonitorPage() {
  const { conn, selection, goto } = useDatabase()
  const { confirm, dialog } = useConfirm()
  if (!conn) return null
  return (
    <Page fill>
      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <MonitorTab
          conn={conn}
          schema={selection.schema}
          confirm={confirm}
          onOpenTable={(schema, table) => goto("/databases/browse", { schema, table })}
        />
      </div>
      {dialog}
    </Page>
  )
}
