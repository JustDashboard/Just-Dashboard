"use client"

import { Page } from "@/components/page"
import { AdvisorTab } from "@/components/database/advisor-tab"
import { useDatabase } from "@/components/database/db-context"

export default function AdvisorPage() {
  const { conn, selection, goto } = useDatabase()
  if (!conn) return null
  return (
    <Page>
      <AdvisorTab
        conn={conn}
        schema={selection.schema}
        onQuery={(sql) => goto("/databases/query", { sql })}
      />
    </Page>
  )
}
