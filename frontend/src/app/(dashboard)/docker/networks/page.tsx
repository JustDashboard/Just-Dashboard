"use client"

import { Suspense, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Plus } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { NetworksTab } from "@/components/docker/networks-tab"
import { Button } from "@/components/ui/button"

export default function DockerNetworksPage() {
  return (
    <Suspense
      fallback={
        <Page>
          <PageContext eyebrow="Docker" title="Networks" />
        </Page>
      }
    >
      <DockerNetworksContent />
    </Suspense>
  )
}

function DockerNetworksContent() {
  const { can } = useAuth()
  const [creating, setCreating] = useState(false)
  const [dismissedId, setDismissedId] = useState("")
  const query = useSearchParams().get("ipamReservation") ?? ""
  const initialReservationId =
    /^[a-f0-9]{32}$/.test(query) && can("system.admin") && dismissedId !== query ? query : ""
  const onCreatingChange = (open: boolean) => {
    setCreating(open)
    if (!open && initialReservationId) setDismissedId(initialReservationId)
  }
  return (
    <Page>
      <PageContext eyebrow="Docker" title="Networks" />
      <NetworksTab
        creating={creating || Boolean(initialReservationId)}
        onCreatingChange={onCreatingChange}
        initialReservationId={initialReservationId}
        actions={
          can("service.control") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              Create network
            </Button>
          )
        }
      />
    </Page>
  )
}
