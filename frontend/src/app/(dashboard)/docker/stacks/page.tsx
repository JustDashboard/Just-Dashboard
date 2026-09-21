"use client"

import { useState } from "react"
import { FolderPlus } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader } from "@/components/page"
import { StacksTab } from "@/components/docker/stacks-tab"
import { Button } from "@/components/ui/button"

export default function DockerStacksPage() {
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
      <StacksTab creating={creating} onCreatingChange={setCreating} />
    </Page>
  )
}
