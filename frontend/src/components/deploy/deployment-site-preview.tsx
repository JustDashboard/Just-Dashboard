"use client"

import { useState } from "react"
import { Globe, RefreshClockwise } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import type { DeploymentSummary } from "@/lib/types"

export function DeploymentSitePreview({ deployment }: { deployment: DeploymentSummary }) {
  const [open, setOpen] = useState(false)
  const [revision, setRevision] = useState(0)
  const [mobile, setMobile] = useState(false)
  let url: URL
  try {
    if (!deployment.endpoint) return null
    url = new URL(
      deployment.endpoint.includes("://") ? deployment.endpoint : `https://${deployment.endpoint}`,
    )
    if (!["https:", "http:"].includes(url.protocol) || url.username || url.password) return null
  } catch {
    return null
  }
  return (
    <Panel className="xl:col-span-2">
      <PanelHeader
        title="Website preview"
        actions={
          <Button variant="outline" size="sm" asChild>
            <a href={url.href} target="_blank" rel="noopener noreferrer">
              Open website
            </a>
          </Button>
        }
      />
      <PanelBody className="space-y-3">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
          <span className="min-w-0 font-mono text-xs break-all text-muted-foreground">
            {url.host}
          </span>
          <div className="flex flex-wrap gap-2">
            {open && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  aria-pressed={mobile}
                  onClick={() => setMobile(!mobile)}
                >
                  {mobile ? "Desktop width" : "Mobile width"}
                </Button>
                <Button
                  size="icon-sm"
                  variant="outline"
                  aria-label="Reload website preview"
                  onClick={() => setRevision(revision + 1)}
                >
                  <RefreshClockwise className="size-4" />
                </Button>
              </>
            )}
            <Button size="sm" variant="outline" onClick={() => setOpen(!open)}>
              <Globe className="size-4" />
              {open ? "Close preview" : "Load preview"}
            </Button>
          </div>
        </div>
        {open && (
          <>
            <div className="flex justify-center overflow-hidden rounded-lg border border-hairline bg-background">
              <iframe
                key={`${deployment.id}:${revision}`}
                src={`/api/v1/deploy/${deployment.id}/preview-frame`}
                title={`Website preview for ${deployment.name}`}
                className={`h-[32rem] ${mobile ? "w-96 max-w-full" : "w-full"}`}
                referrerPolicy="no-referrer"
              />
            </div>
            <p className="text-xs text-muted-foreground">
              The preview connects from your browser. If the site blocks embedding, requires
              sign-in, or uses HTTP while this dashboard uses HTTPS, open the website directly.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
