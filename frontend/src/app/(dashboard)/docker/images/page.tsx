"use client"

import { useState } from "react"
import { Download, Wrench } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { ImagesTab } from "@/components/docker/images-tab"
import { Button } from "@/components/ui/button"

export default function DockerImagesPage() {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const [pulling, setPulling] = useState<string | null>(null)
  const [building, setBuilding] = useState(false)
  return (
    <Page>
      <PageContext eyebrow="Docker" title="Images" />
      <ImagesTab
        confirm={confirm}
        pulling={pulling}
        onPullingChange={setPulling}
        building={building}
        onBuildingChange={setBuilding}
        actions={
          can("service.control") && (
            <span className="flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" onClick={() => setPulling("")}>
                <Download className="size-4" />
                Pull image
              </Button>
              <Button size="sm" variant="outline" onClick={() => setBuilding(true)}>
                <Wrench className="size-4" />
                Build image
              </Button>
            </span>
          )
        }
      />
      {dialog}
    </Page>
  )
}
