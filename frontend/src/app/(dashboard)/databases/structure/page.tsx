"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { StructureTab } from "@/components/database/structure-tab"
import { useDatabase } from "@/components/database/db-context"

export default function StructurePage() {
  const { conn, info, selection, select } = useDatabase()
  const { confirm, dialog } = useConfirm()
  if (!conn) return null
  return (
    <Page fill>
      <StructureTab
        conn={conn}
        info={info}
        confirm={confirm}
        schema={selection.schema}
        table={selection.table}
        onSelect={(s) => select({ schema: s?.schema ?? "", table: s?.table ?? null })}
      />
      {dialog}
    </Page>
  )
}
