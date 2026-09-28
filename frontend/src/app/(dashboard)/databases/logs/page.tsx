"use client"

import { Page } from "@/components/page"
import { LogsTab } from "@/components/database/logs-tab"
import { useDatabase } from "@/components/database/db-context"

export default function LogsPage() {
  const { conn, goto } = useDatabase()
  if (!conn) return null
  return (
    <Page fill>
      <LogsTab conn={conn} onQuery={(sql) => goto("/databases/query", { sql })} />
    </Page>
  )
}
