"use client"

import { useEffect } from "react"
import Link from "next/link"
import { RotateClockwise } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { DATABASES_HREF } from "@/components/database/shell/routes"

/**
 * A page of a database that threw while rendering.
 *
 * The boundary sits inside the database's layout, so what is lost is the one
 * page: the rail still lists the database's other pages and the strip still
 * says which database this is. The dashboard-wide boundary above would have
 * taken both with it, and left the reader of a broken Data page with no way
 * to its Settings but the address bar.
 */
export default function DatabasePageError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    // The console is the only place the stack survives; the panel shows the
    // message, which is what the reader can act on.
    console.error("database page render failed", error)
  }, [error])

  return (
    <Page>
      <PageContext eyebrow="Databases" title="This page stopped rendering" />
      {/* Framed: it stands where a page would, alone, and has to read as one
          thing that happened rather than as part of the page that is gone. */}
      <Panel>
        <PanelHeader
          title="Something in this page threw"
          actions={
            <>
              <Button size="sm" variant="outline" asChild>
                <Link href={DATABASES_HREF}>All databases</Link>
              </Button>
              <Button size="sm" onClick={reset}>
                <RotateClockwise className="size-3.5" />
                Try again
              </Button>
            </>
          }
        />
        <PanelBody className="space-y-3">
          <p className="text-body leading-relaxed">
            This is a bug in the dashboard rather than a problem with the database. The other pages
            of this database are still in the rail; trying again is worth one attempt.
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
