"use client"

import { Page, PageContext } from "@/components/page"
import { SessionsView, useSessions } from "@/components/account/sessions"

/**
 * Where this account is signed in, read to answer one question: is every one
 * of these me? So the page opens on the session it is being read through,
 * drawn as the browser and system it is and the network it came from; then
 * every session as the path it takes to this server — the browser, the
 * network, the server — with a wire that pulses while it is open and goes
 * amber where a password alone let it in; then every other session as a row,
 * with when it was last seen in words and the one command that ends it.
 *
 * The rows carried each session's life as a bar on a shared axis from its
 * sign-in to its expiry, and the operator asked for it to go: a dot on a
 * dashed line said less than "seen 5h ago" beside it already did.
 */
export default function AccountSessionsPage() {
  const sessions = useSessions()
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Sessions" />
      <SessionsView sessions={sessions} />
    </Page>
  )
}
