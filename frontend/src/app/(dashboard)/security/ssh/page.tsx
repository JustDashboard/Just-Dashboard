"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { SSHPanel } from "@/components/security/ssh-panel"

// The directive a link names lives in the query string, which the App Router
// only hands out inside a Suspense boundary.
export default function SecuritySSHPage() {
  return (
    <Page className="animate-rise">
      <Suspense>
        <SSHPanel />
      </Suspense>
    </Page>
  )
}
