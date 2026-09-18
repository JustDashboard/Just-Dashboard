"use client"

import { useState } from "react"
import { External, Globe, RefreshClockwise } from "@/components/icons"
import { API_BASE } from "@/lib/api"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { deploymentURL, hostOf, WorkloadMark } from "@/components/deploy/vocabulary"
import type { DeploymentSummary } from "@/lib/types"

/**
 * The one framed block on the overview: a window you look through at the
 * running site, ported from the pre-rebuild `DeploymentSitePreview` with the
 * same iframe behaviour (design system §15 keeps this one frame — a preview
 * is the exception written down in spec §1.1).
 */
export function SitePreview({ deployment }: { deployment: DeploymentSummary }) {
  const url = deploymentURL(deployment.endpoint)
  const [open, setOpen] = useState(Boolean(deployment.liveReleaseId && url))
  const [revision, setRevision] = useState(0)
  const [mobile, setMobile] = useState(false)

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
          {url ? hostOf(url) : deployment.name}
        </span>
        {open && (
          <IconAction label="Reload website preview" onClick={() => setRevision(revision + 1)}>
            <RefreshClockwise />
          </IconAction>
        )}
      </div>
      {open && url ? (
        <div className="flex justify-center">
          <iframe
            key={`${deployment.id}:${revision}`}
            src={`${API_BASE}/deploy/${deployment.id}/preview-frame`}
            title={`Website preview for ${deployment.name}`}
            className={mobile ? "h-72 w-80 max-w-full" : "h-72 w-full"}
            referrerPolicy="no-referrer"
            loading="lazy"
          />
        </div>
      ) : (
        <div className="flex min-h-64 flex-col items-center justify-center gap-4 px-6 py-8 text-center">
          <WorkloadMark profile={deployment.profile} />
          <p className="text-body font-medium">
            {url
              ? "Your application, at a glance"
              : deployment.liveReleaseId
                ? "Running on your server"
                : "Deploy your project to bring it to life here"}
          </p>
          {url && (
            <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
              <Globe className="size-3.5" /> Load preview
            </Button>
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
              className="inline-flex items-center gap-1 rounded-sm text-xs text-muted-foreground focus-ring hover:text-foreground hover:underline"
            >
              Open website <External className="size-3" />
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
