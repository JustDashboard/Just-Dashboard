import type { Metadata } from "next"
import Link from "next/link"
import { StandaloneState } from "@/components/standalone-state"
import { Button } from "@/components/ui/button"

export const metadata: Metadata = { title: "Page not found · Just Dashboard" }

/**
 * An address nothing answers, outside the dashboard's shell. Addresses that
 * could be dashboard pages are caught by `(dashboard)/[...missing]` first, and
 * keep the rail; this is for what is thrown from the pages that have no shell.
 */
export default function NotFound() {
  return (
    <StandaloneState
      eyebrow="Not found"
      title="Page not found"
      description="There is nothing at this address. It may have moved in an update, or the link may be mistyped."
      actions={
        <Button size="sm" variant="outline" asChild>
          <Link href="/">Back to the Overview</Link>
        </Button>
      }
    />
  )
}
