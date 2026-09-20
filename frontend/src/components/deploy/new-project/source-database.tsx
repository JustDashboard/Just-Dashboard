"use client"

import { FlowPanel } from "@/components/flow"
import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"

/**
 * A database is infrastructure, not a deployment project: it never reaches
 * Configure. Provisioning here is exactly `DatabaseQuickDeploy`'s own flow —
 * an engine, two fields, and a connection string to take away or hand to
 * another project's "Add database".
 *
 * The `FlowPanel` is here rather than inside `DatabaseQuickDeploy` because
 * that component is shared with a project's own Databases page, which is a
 * reading page and must stay flat (§16). What it draws is a `Panel plain` —
 * header, body, footer, and no frame of its own — so wrapping it supplies
 * this tab's one foreground without nesting a frame inside a frame. Every
 * other source tab raises the thing being decided; without this, Database
 * was the one tab on the chooser with nothing in front of the page.
 */
export function SourceDatabase() {
  return (
    <FlowPanel className="w-full max-w-3xl min-w-0 p-4">
      <DatabaseQuickDeploy />
    </FlowPanel>
  )
}
