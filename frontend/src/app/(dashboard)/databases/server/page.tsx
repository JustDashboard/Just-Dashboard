"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { ServerTab } from "@/components/database/server-tab"
import { useDatabase } from "@/components/database/db-context"

export default function ServerPage() {
  const { conn, info, refreshConnections, goto } = useDatabase()
  const { confirm, dialog } = useConfirm()
  if (!conn) return null
  return (
    <Page className="animate-rise">
      <ServerTab
        conn={conn}
        info={info}
        confirm={confirm}
        onConnected={(id) => {
          refreshConnections()
          goto("/databases/overview", { conn: id })
        }}
      />
      {dialog}
    </Page>
  )
}
