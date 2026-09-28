"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { BackupsTab } from "@/components/database/backups-tab"
import { useDatabase } from "@/components/database/db-context"

export default function BackupsPage() {
  const { conn } = useDatabase()
  const { confirm, dialog } = useConfirm()
  if (!conn) return null
  return (
    <Page>
      <BackupsTab conn={conn} confirm={confirm} />
      {dialog}
    </Page>
  )
}
