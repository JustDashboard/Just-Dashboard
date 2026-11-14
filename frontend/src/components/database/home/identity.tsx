"use client"

import { DatabaseHeader } from "@/components/database/shell/database-header"

/**
 * The line a database's home opens on: the database's header
 * (`shell/database-header.tsx`), drawn by the home at the top of its own page.
 */
export function HomeIdentity() {
  return <DatabaseHeader />
}
