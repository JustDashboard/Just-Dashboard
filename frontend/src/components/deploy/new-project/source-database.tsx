"use client"

import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"

/**
 * A database is infrastructure, not a deployment project: it never reaches
 * Configure. Provisioning here is exactly `DatabaseQuickDeploy`'s own flow —
 * an engine, two fields, and a connection string to take away or hand to
 * another project's "Add database".
 */
export function SourceDatabase() {
  return (
    <div className="mx-auto w-full max-w-2xl">
      <DatabaseQuickDeploy />
    </div>
  )
}
