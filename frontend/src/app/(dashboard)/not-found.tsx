"use client"

import { MissingPage } from "@/components/missing-page"
import { Page, PageContext } from "@/components/page"

/** A missing page, with the rail still there to go somewhere else. */
export default function DashboardNotFound() {
  return (
    <Page className="animate-rise">
      <PageContext title="Page not found" />
      <MissingPage />
    </Page>
  )
}
