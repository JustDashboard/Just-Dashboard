"use client"

import { useState } from "react"
import { Plus } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader } from "@/components/page"
import { NetworksTab } from "@/components/docker/networks-tab"
import { Button } from "@/components/ui/button"

export default function DockerNetworksPage() {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const [creating, setCreating] = useState(false)
  return (
    <Page>
      <PageHeader
        eyebrow="Docker"
        title="Networks"
        actions={
          can("service.control") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              Create network
            </Button>
          )
        }
      />
      <NetworksTab confirm={confirm} creating={creating} onCreatingChange={setCreating} />
      {dialog}
    </Page>
  )
}
