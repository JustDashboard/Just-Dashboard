"use client"

import { useState } from "react"
import { Globe, RefreshClockwise } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { deploymentURL, WorkloadMark } from "@/components/deploy/deployment-ui"
import type { DeploymentSummary } from "@/lib/types"

export function DeploymentSitePreview({ deployment }: { deployment: DeploymentSummary }) {
  const [open, setOpen] = useState(
    Boolean(deployment.liveReleaseId && deploymentURL(deployment.endpoint)),
  )
  const [revision, setRevision] = useState(0)
  const [mobile, setMobile] = useState(false)
  const url = deploymentURL(deployment.endpoint)
  return (
    <section
      aria-label="Website preview"
      className="min-w-0 overflow-hidden rounded-lg border border-hairline bg-background"
    >
      <div className="flex min-h-10 items-center gap-3 border-b border-hairline bg-surface-header px-3">
        <span aria-hidden="true" className="flex shrink-0 gap-1">
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
        </span>
        <span className="min-w-0 flex-1 truncate text-center font-mono text-hint text-muted-foreground">
          {url ? new URL(url).host : deployment.name}
        </span>
        {open && (
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Reload website preview"
            onClick={() => setRevision(revision + 1)}
          >
            <RefreshClockwise className="size-3" />
          </Button>
        )}
      </div>
      {open && url ? (
        <div className="flex justify-center">
          <iframe
            key={`${deployment.id}:${revision}`}
            src={`/api/v1/deploy/${deployment.id}/preview-frame`}
            title={`Website preview for ${deployment.name}`}
            className={`h-72 ${mobile ? "w-80 max-w-full" : "w-full"}`}
            referrerPolicy="no-referrer"
            loading="lazy"
          />
        </div>
      ) : (
        <div className="flex min-h-64 flex-col items-center justify-center gap-4 px-6 py-8 text-center">
          <WorkloadMark profile={deployment.profile} />
          <p className="text-sm font-medium">
            {url
              ? "Your application, at a glance"
              : deployment.liveReleaseId
                ? "Running on your server"
                : "Ready when you are"}
          </p>
          {url ? (
            <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
              <Globe className="size-3.5" /> Load preview
            </Button>
          ) : (
            <p className="max-w-xs text-xs leading-relaxed text-muted-foreground">
              {deployment.liveReleaseId
                ? "This workload has no public website. Open Runtime to view its services."
                : "Deploy your project to bring it to life here."}
            </p>
          )}
        </div>
      )}
      {open && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hairline px-3 py-2">
          <Button
            size="xs"
            variant="ghost"
            aria-pressed={mobile}
            onClick={() => setMobile(!mobile)}
          >
            {mobile ? "Desktop width" : "Mobile width"}
          </Button>
          <Button size="xs" variant="ghost" onClick={() => setOpen(false)}>
            Close preview
          </Button>
          {url && (
            <a
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-xs text-muted-foreground underline focus-ring"
            >
              Open website
            </a>
          )}
          <p className="w-full text-hint text-muted-foreground">
            If your site blocks embedding, open it directly.
          </p>
        </div>
      )}
    </section>
  )
}
