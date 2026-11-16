"use client"

import Link from "next/link"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * An address under a database that is none of its pages: a mistyped section,
 * a page from a newer release, a path with something after it.
 *
 * Without this the framework answers with its own bare 404, outside the
 * dashboard altogether — no rail, no header, nothing to press. The database
 * itself was found, so the page says which part of the address was not and
 * stays inside it: the rail still lists its pages and the way to its home is
 * one press. It is drawn as the engine the database is, so the statement
 * names what it belongs to; it was a question mark on a plate.
 *
 * It is not the dashboard's `MissingPage` (`components/missing-page.tsx`),
 * which draws the address asked for against this server's nearest pages: the
 * database's own rail is the nearest pages here. It had the same name, and
 * the two were taken for one another.
 */
export function DatabaseMissingPage() {
  const { conn, engine, href } = useDatabase()
  return (
    <Page rise>
      <PageContext title={`${conn.name} · Page not found`} />
      <EmptyState
        mark={<EngineMark engine={engine} />}
        title="Page not found"
        description={`${conn.name} has no page at this address. Its pages are in the rail.`}
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={href("home")}>Open the database&apos;s Home</Link>
          </Button>
        }
      />
    </Page>
  )
}
