"use client"

import Link from "next/link"
import { ArrowLeft, Plus } from "@/components/icons"
import { FlowHeader } from "@/components/flow"
import { Page } from "@/components/page"
import { EmptyState } from "@/components/state"
import { DATABASES_HREF } from "@/components/database/shell/routes"

/**
 * Add a database: start one in a container, connect one that already runs
 * somewhere, or take one found on this machine. A sequence with an outcome,
 * so the page is drawn in the flow register (§16).
 *
 * It opens on `?mode=start|connect|found`, and on one found server with
 * `?key=` — see `newDatabaseHref` in `shell/routes.ts`.
 */
export function AddDatabase() {
  return (
    <Page register="flow">
      <FlowHeader
        eyebrow={
          <Link
            href={DATABASES_HREF}
            className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> Databases
          </Link>
        }
        question="Where is the database?"
      />
      <EmptyState
        icon={Plus}
        title="Add a database"
        description={
          <>
            Start one here, connect one that runs elsewhere, or take one found on this server.{" "}
            <span className="font-mono text-hint">components/database/connect</span>
          </>
        }
      />
    </Page>
  )
}
