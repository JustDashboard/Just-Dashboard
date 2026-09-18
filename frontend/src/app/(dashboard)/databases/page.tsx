"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { BrowseTab } from "@/components/database/browse-tab"
import { RedisBrowser } from "@/components/database/redis-browser"
import { MongoBrowser } from "@/components/database/mongo-browser"
import { useDatabase } from "@/components/database/db-context"

export default function BrowsePage() {
  const { conn, info, selection, select } = useDatabase()
  const { confirm, dialog } = useConfirm()
  if (!conn) return null

  const sel = selection.table ? { schema: selection.schema, table: selection.table } : null

  // The SQL browser is a workbench sized to the window; the key and document
  // browsers are pages of panels that scroll.
  if (conn.driver === "redis" || conn.driver === "mongodb") {
    return (
      <Page>
        {conn.driver === "redis" ? (
          <RedisBrowser conn={conn} confirm={confirm} />
        ) : (
          <MongoBrowser conn={conn} confirm={confirm} />
        )}
        {dialog}
      </Page>
    )
  }

  return (
    <Page fill>
      <BrowseTab
        conn={conn}
        info={info}
        confirm={confirm}
        selection={sel}
        onSelect={(s) => select({ schema: s?.schema ?? "", table: s?.table ?? null })}
      />
      {dialog}
    </Page>
  )
}
