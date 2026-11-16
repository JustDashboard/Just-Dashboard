"use client"

import { policyWords, scopeName } from "@/lib/account-link"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { Detail, DetailList } from "@/components/detail-list"
import { Section } from "@/components/section"

/**
 * The automatic-update policy, read-only. The site — or, on Settings → Hub, the hub — stores and
 * edits it and this server runs it from its own update checker, so a peer's outage never blocks
 * or triggers an update; the page only mirrors what was last set, and says where to change it.
 */
export function Policy({
  policy,
  scope,
  site,
  peer = "site",
  className,
}: {
  policy: NonNullable<AccountLinkStatus["link"]>["updatePolicy"]
  scope: AccountLinkScope
  /** Where it is set, by name: "just-dashboard.com", "hub.tailed39ba.ts.net". */
  site: string
  /** What that is, in the sentence about who may change it. */
  peer?: "site" | "hub"
  className?: string
}) {
  const words = policyWords(policy)
  return (
    <Section title="Automatic updates" className={className}>
      <DetailList ruled labelWidth="fixed" size="body">
        <Detail label="Installs">{words.auto}</Detail>
        {words.window && <Detail label="When">{words.window}</Detail>}
      </DetailList>
      <p className="text-xs text-muted-foreground">
        Set from {site}.{" "}
        {scope === "maintain"
          ? "This server applies it itself."
          : `The ${peer} can change it only at the ${scopeName("maintain")} scope.`}
      </p>
    </Section>
  )
}
