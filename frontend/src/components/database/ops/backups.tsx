"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** Dumps, restore and the schedule. */
export function Backups() {
  return (
    <AreaStub section="backups" area="ops">
      Dumps of this database: take one, restore one, and the schedule that takes them.
    </AreaStub>
  )
}
