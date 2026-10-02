"use client"

import { useEffect } from "react"
import { useParams, usePathname, useRouter, useSearchParams } from "next/navigation"
import { PageState } from "@/components/page"
import { DatabaseNotFound, DatabaseProvider } from "@/components/database/shell/database-context"
import { DatabaseShell } from "@/components/database/shell/database-shell"
import { DATABASE_ID, legacyDatabasesHref } from "@/components/database/shell/routes"

/**
 * One database: `/databases/<id>` and every page under it.
 *
 * The id in the path is the connection, and it is the only thing that
 * chooses one. The provider resolves it against the saved connections — an
 * id none of them has is "Database not found", never some other database —
 * and the shell publishes that database's pages to the rail and draws its
 * strip over whichever of them is open.
 *
 * A segment that is not a number is an address from before the connection
 * moved into the path (`/databases/browse?conn=3`), still stored in boards
 * and bookmarks; it is sent on to the page that took its work.
 */
export default function DatabaseLayout({ children }: { children: React.ReactNode }) {
  const { id } = useParams<{ id: string }>()
  if (!DATABASE_ID.test(id)) return <FormerAddress />
  return (
    // Keyed on the id: what the provider holds about one database — its
    // status, a write on its way to the address — is not another's.
    <DatabaseProvider key={id} id={Number(id)}>
      <DatabaseShell>{children}</DatabaseShell>
    </DatabaseProvider>
  )
}

function FormerAddress() {
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()
  const target = legacyDatabasesHref(pathname, params)
  useEffect(() => {
    // Replaced, not pushed: the old address should not be what Back returns to.
    if (target) router.replace(target)
  }, [router, target])
  if (target) return <PageState eyebrow="Apps" title="Databases" />
  return <DatabaseNotFound />
}
