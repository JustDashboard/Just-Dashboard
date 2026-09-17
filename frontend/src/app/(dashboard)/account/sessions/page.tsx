"use client"

import { Page, PageHeader } from "@/components/page"
import { SessionsTable, SignOutOthersButton, useSessions } from "@/components/account/sessions"

export default function AccountSessionsPage() {
  const sessions = useSessions()
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="Sessions"
        actions={<SignOutOthersButton sessions={sessions} />}
      />
      <SessionsTable sessions={sessions} />
    </Page>
  )
}
