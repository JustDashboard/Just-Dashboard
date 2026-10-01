"use client"

import Link from "next/link"
import { Question } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * An address under a database that is none of its pages: a mistyped section,
 * a page from a newer release, a path with something after it.
 *
 * Without this the framework answers with its own bare 404, outside the
 * dashboard altogether — no rail, no strip, nothing to press. The database
 * itself was found, so the page says which part of the address was not and
 * stays inside it: the rail still lists its pages and the way to its home is
 * one press.
 */
export function MissingPage() {
  const { conn, href } = useDatabase()
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Databases" title={`${conn.name} · Page not found`} />
      <EmptyState
        icon={Question}
        title="Page not found"
        description={`${conn.name} has no page at this address. Its pages are in the rail.`}
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={href("home")}>Open Home</Link>
          </Button>
        }
      />
    </Page>
  )
}
