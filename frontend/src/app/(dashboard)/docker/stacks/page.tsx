"use client"

import { useState } from "react"
import { FolderPlus } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader } from "@/components/page"
import { StacksTab } from "@/components/docker/stacks-tab"
import { Button } from "@/components/ui/button"

export default function DockerStacksPage() {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const [creating, setCreating] = useState(false)
  return (
    <Page>
      <PageHeader
        eyebrow="Docker"
        title="Stacks"
        actions={
          can("system.admin") &&
          can("file.write") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <FolderPlus className="size-4" />
              Create stack
            </Button>
          )
        }
      />
      <StacksTab confirm={confirm} creating={creating} onCreatingChange={setCreating} />
      {dialog}
    </Page>
  )
}
