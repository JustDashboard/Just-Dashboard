"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** Sessions, locks, statements, table statistics and maintenance on a SQL server. */
export function SqlPerformance() {
  return (
    <AreaStub section="performance" area="ops">
      What the server is doing: sessions, locks, the statements that cost the most, table statistics
      and maintenance.
    </AreaStub>
  )
}
