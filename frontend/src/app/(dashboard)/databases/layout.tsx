"use client"

import { DatabasesProvider } from "@/components/database/shell/databases-context"

/**
 * The Databases section: the saved connections and the driver catalogue,
 * read once here and shared by every page below — the control center, the
 * map, the way to add one, and each database's own pages.
 *
 * It draws nothing of its own. Which database a page is about is in the
 * address (`/databases/<id>/…`), and the layout of that segment is what
 * resolves it; this one only keeps the list, and keeps its children mounted
 * whatever a poll does.
 */
export default function DatabasesLayout({ children }: { children: React.ReactNode }) {
  return <DatabasesProvider>{children}</DatabasesProvider>
}
