"use client"

import { policyWords, scopeName } from "@/lib/account-link"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { Reading, ReadingList } from "@/components/account-link/reading"
import { Section } from "@/components/page"

/**
 * The automatic-update policy, read-only. The site stores and edits it and this server runs it
 * from its own update checker, so a site outage never blocks or triggers an update; the page
 * only mirrors what was last set, and says where to change it.
 */
export function Policy({
  policy,
  scope,
  site,
  className,
}: {
  policy: NonNullable<AccountLinkStatus["link"]>["updatePolicy"]
  scope: AccountLinkScope
  site: string
  className?: string
}) {
  const words = policyWords(policy)
  return (
    <Section title="Automatic updates" className={className}>
      <ReadingList>
        <Reading label="Installs">{words.auto}</Reading>
        {words.window && <Reading label="When">{words.window}</Reading>}
      </ReadingList>
      <p className="text-xs text-muted-foreground">
        Set from {site}.{" "}
        {scope === "maintain"
          ? "This server applies it itself."
          : `The site can change it only at the ${scopeName("maintain")} scope.`}
      </p>
    </Section>
  )
}
