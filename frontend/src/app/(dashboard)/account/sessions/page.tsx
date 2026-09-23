"use client"

import { Page, PageHeader } from "@/components/page"
import { SessionsView, SignOutOthersButton, useSessions } from "@/components/account/sessions"

/**
 * Where this account is signed in, read to answer one question: is every one
 * of these me? So the page opens on the session it is being read through,
 * drawn as the browser and system it is and the network it came from, and
 * every other session is a row drawn the same way beneath it — Chrome on
 * macOS as Chrome's mark over Apple's, an address on the tailnet as
 * Tailscale's. It was a framed table of user-agent strings and addresses.
 */
export default function AccountSessionsPage() {
  const sessions = useSessions()
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="Sessions"
        actions={<SignOutOthersButton sessions={sessions} />}
      />
      <SessionsView sessions={sessions} />
    </Page>
  )
}
