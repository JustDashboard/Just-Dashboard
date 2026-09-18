"use client"

import { Page } from "@/components/page"
import { useAuth } from "@/hooks/use-auth"
import { ErDiagram } from "@/components/database/er-diagram"
import { useDatabase } from "@/components/database/db-context"

export default function DiagramPage() {
  const { conn, selection, goto } = useDatabase()
  const { can } = useAuth()
  if (!conn) return null
  return (
    <Page fill>
      <ErDiagram
        conn={conn}
        schema={selection.schema}
        canSave={can("service.control")}
        onOpenTable={(schema, table) => goto("/databases", { schema, table })}
        onOpenStructure={(schema, table) => goto("/databases/structure", { schema, table })}
        onQuery={(sql) => goto("/databases/query", { sql })}
      />
    </Page>
  )
}
