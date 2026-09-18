"use client"

import { useEffect } from "react"
import { RotateClockwise } from "@/components/icons"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Button } from "@/components/ui/button"

/**
 * The blast radius of a render error.
 *
 * Without this, one component reading a field that arrived as `null` takes the
 * entire dashboard to a blank white page — no nav, no way back, nothing on
 * screen to say what happened. That is exactly what a closed detail panel
 * fetching a list endpoint did: `data.services.map` on an array, one
 * TypeError, whole app gone.
 *
 * The underlying bug is fixed and the API no longer emits `null` where the
 * client expects a list. This is the floor under the next one: a route segment
 * that throws now loses that segment and keeps the shell, and says what broke
 * rather than showing nothing. Next's App Router mounts this automatically for
 * everything under `(dashboard)`.
 *
 * `reset()` re-renders the segment from scratch, which is genuinely the fix for
 * the common case — a transient bad payload that the next poll replaces.
 */
export default function DashboardError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    // The browser console is the only place a stack trace survives; the panel
    // below deliberately shows the message rather than the trace, which is
    // noise to everyone but whoever is debugging it.
    console.error("dashboard render failed", error)
  }, [error])

  return (
    <Page>
      <PageHeader eyebrow="Error" title="This page stopped rendering" />
      <Panel>
        <PanelHeader
          title="Something in this page threw"
          actions={
            <Button size="sm" onClick={reset}>
              <RotateClockwise className="size-3.5" />
              Try again
            </Button>
          }
        />
        <PanelBody className="space-y-3">
          <p className="text-body leading-relaxed">
            This is a bug in the dashboard rather than a problem with your server. Trying again is
            worth one attempt — most of these come from a single bad response that the next request
            replaces.
          </p>
          <Well className="whitespace-pre-wrap">
            {error.message}
            {error.digest ? `\n\ndigest: ${error.digest}` : ""}
          </Well>
        </PanelBody>
      </Panel>
    </Page>
  )
}
