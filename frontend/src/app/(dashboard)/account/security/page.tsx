"use client"

import { Page, PageHeader } from "@/components/page"
import { PasswordPanel, TwoFactorPanel } from "@/components/account/security-panels"

export default function AccountSecurityPage() {
  return (
    <Page className="animate-rise">
      <PageHeader eyebrow="Account" title="Security" />
      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <PasswordPanel />
        <TwoFactorPanel />
      </div>
    </Page>
  )
}
