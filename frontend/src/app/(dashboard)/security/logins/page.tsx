"use client"

import { Page } from "@/components/page"
import { LoginsPanels } from "@/components/security/logins-panels"

export default function SecurityLoginsPage() {
  return (
    <Page className="animate-rise">
      <LoginsPanels />
    </Page>
  )
}
