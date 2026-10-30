"use client"

import Link from "next/link"
import { Question } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"

/** A missing page, with the rail still there to go somewhere else. */
export default function DashboardNotFound() {
  return (
    <Page className="animate-rise">
      <PageContext title="Page not found" />
      <EmptyState
        icon={Question}
        title="Page not found"
        description="There is nothing at this address. It may have moved in an update, or the link may be mistyped."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href="/">Back to the Overview</Link>
          </Button>
        }
      />
    </Page>
  )
}
