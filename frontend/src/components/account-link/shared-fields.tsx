"use client"

import { useId } from "react"
import {
  NEVER_SENT,
  scopeName,
  sharedGroups,
  sharedItems,
  type SharedGroup,
} from "@/lib/account-link"
import { relativeTime, timestamp } from "@/lib/format"
import type { AccountLinkScope } from "@/lib/types"
import { Reading, ReadingList } from "@/components/account-link/reading"
import { Section } from "@/components/page"

/**
 * What leaves this machine.
 *
 * Once linked it is the last heartbeat itself, read back as the groups it was sent in, rather than
 * a description of it: the page is the place a person checks that "observe" shared what it said
 * it would. Before there is a heartbeat — choosing a scope, waiting for approval, a link younger
 * than thirty seconds — the same question is answered in words for the scope in question.
 */
export function SharedFields({
  scope,
  snapshot,
  sentAt,
}: {
  scope: AccountLinkScope
  snapshot?: Record<string, unknown>
  sentAt?: string
}) {
  const groups = sharedGroups(snapshot)
  if (groups.length === 0) {
    return (
      <Section title="What this server will send">
        <div className="grid min-w-0 gap-x-12 gap-y-4 xl:grid-cols-2">
          <div className="min-w-0 space-y-2">
            <p className="eyebrow">At {scopeName(scope)}</p>
            <ul className="divide-y divide-hairline text-body">
              {sharedItems(scope).map((item) => (
                <li key={item} className="py-2">
                  {item}
                </li>
              ))}
            </ul>
          </div>
          <Withheld />
        </div>
      </Section>
    )
  }
  return (
    <Section
      title="What this server shares"
      actions={
        sentAt && (
          <span className="text-hint text-muted-foreground">last sent {relativeTime(sentAt)}</span>
        )
      }
    >
      <div className="grid min-w-0 gap-x-12 gap-y-8 xl:grid-cols-2">
        {groups.map((group) => (
          <Group key={group.key} group={group} />
        ))}
      </div>
      <Withheld />
    </Section>
  )
}

function Withheld() {
  return (
    <p className="min-w-0 text-xs leading-relaxed text-muted-foreground">
      <span className="eyebrow mr-2">Never sent</span>
      {NEVER_SENT}.
    </p>
  )
}

function Group({ group }: { group: SharedGroup }) {
  const heading = useId()
  return (
    <section aria-labelledby={heading} className="min-w-0 content-start">
      <h3 id={heading} className="eyebrow border-b border-hairline pb-2">
        {group.label}
      </h3>
      <ReadingList>
        {group.rows.map((row, i) => (
          <Reading key={`${row.label}-${i}`} label={row.label} mono>
            {row.time ? timestamp(row.time) : row.value}
            {row.items && (
              <ul className="mt-1 space-y-0.5 font-sans text-hint text-muted-foreground">
                {row.items.map((item, n) => (
                  <li key={n} className="break-words">
                    {item}
                  </li>
                ))}
              </ul>
            )}
          </Reading>
        ))}
      </ReadingList>
    </section>
  )
}
