"use client"

import { Page } from "@/components/page"
import { SearchTab } from "@/components/database/search-tab"
import { useDatabase } from "@/components/database/db-context"

export default function FindPage() {
  const { conn, selection, goto } = useDatabase()
  if (!conn) return null
  return (
    <Page fill>
      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <SearchTab
          conn={conn}
          schema={selection.schema}
          onOpenTable={(schema, table) => goto("/databases/browse", { schema, table })}
        />
      </div>
    </Page>
  )
}
