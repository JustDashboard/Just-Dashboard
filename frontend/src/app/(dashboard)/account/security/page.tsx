"use client"

import { Page, PageContext } from "@/components/page"
import { FormSections } from "@/components/form"
import {
  PasswordSection,
  SessionsSection,
  TwoFactorSection,
} from "@/components/account/security-panels"
import { useSessions } from "@/components/account/sessions"

/**
 * What stands between a session and this server: the second factor first,
 * because "am I protected" is the question the page is opened with, then the
 * password, then where it is signed in. A page that is a form, so its heads
 * sit in a rail with their state under them (§7), the way Configuration's do;
 * it was two panels side by side, each opening on a paragraph.
 */
export default function AccountSecurityPage() {
  const sessions = useSessions()
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Security" />
      <FormSections>
        <TwoFactorSection />
        <PasswordSection />
        <SessionsSection sessions={sessions} />
      </FormSections>
    </Page>
  )
}
