"use client"

import { useId } from "react"
import { Archive, Bell, Clock, Gauge, Heart, Rocket, Slash, Terminal } from "@/components/icons"
import {
  NEVER_SENT,
  scopeName,
  sharedGroups,
  sharedItems,
  type SharedGroup,
} from "@/lib/account-link"
import { relativeTime, timestamp } from "@/lib/format"
import type { AccountLinkScope } from "@/lib/types"
import { MarkTile, type Mark } from "@/components/account-link/marks"
import { Reading, ReadingList } from "@/components/account-link/reading"
import { Section } from "@/components/page"
import { platformProduct } from "@/components/product-logo"

const ITEM_MARK: Record<string, Mark> = {
  version: { brand: true },
  os: { product: "linux" },
  uptime: { glyph: Clock, hue: "var(--tag-blue)" },
  access: { product: "tailscale" },
  metrics: { glyph: Gauge, hue: "var(--tag-cyan)" },
  counts: { product: "docker" },
  alerts: { glyph: Bell, hue: "var(--tag-pink)" },
  names: { product: "docker-compose" },
}

function groupMark(key: string, snapshot?: Record<string, unknown>): Mark {
  const section = (snapshot?.[key] ?? {}) as Record<string, unknown>
  switch (key) {
    case "server":
      return section.os
        ? { product: platformProduct(String(section.os).split(" ")[0]) ?? "linux" }
        : { brand: true }
    case "access":
      return section.kind === "tailscale"
        ? { product: "tailscale" }
        : { glyph: Terminal, hue: "var(--tag-slate)" }
    case "metrics":
      return ITEM_MARK.metrics
    case "containers":
      return { product: "docker" }
    case "stacks":
      return { product: "docker-compose" }
    case "backups":
      return { glyph: Archive, hue: "var(--tag-violet)" }
    case "deployments":
      return { glyph: Rocket, hue: "var(--tag-blue)" }
    case "alerts":
      return ITEM_MARK.alerts
    default:
      return { glyph: Heart, hue: "var(--tag-green)" }
  }
}

/**
 * What leaves this machine.
 *
 * Once linked it is the last heartbeat itself, read back as the groups it was sent in, rather than
 * a description of it: the page is the place a person checks that "observe" shared what it said
 * it would. Before there is a heartbeat — choosing a scope, waiting for approval, a link younger
 * than thirty seconds — the same question is answered for the scope in question, one thing to a
 * line with the mark of what it is read from, so the names Operate adds arrive as a line of their
 * own when it is picked.
 */
export function SharedFields({
  scope,
  snapshot,
  sentAt,
  className,
}: {
  scope: AccountLinkScope
  snapshot?: Record<string, unknown>
  sentAt?: string
  className?: string
}) {
  const groups = sharedGroups(snapshot)
  if (groups.length === 0) {
    return (
      <Section
        title="What this server will send"
        className={className}
        actions={<span className="text-xs text-muted-foreground">At {scopeName(scope)}</span>}
      >
        <ul className="grid min-w-0 gap-x-8 gap-y-4 sm:grid-cols-2 xl:grid-cols-4">
          {sharedItems(scope).map((item) => (
            <li key={item.key} className="flex min-w-0 animate-rise items-center gap-3 text-body">
              <MarkTile mark={ITEM_MARK[item.key] ?? {}} />
              <span className="min-w-0">{item.label}</span>
            </li>
          ))}
        </ul>
        <Withheld />
      </Section>
    )
  }
  return (
    <Section
      title="What this server shares"
      className={className}
      actions={
        sentAt && (
          <span className="text-xs text-muted-foreground">Last sent {relativeTime(sentAt)}</span>
        )
      }
    >
      <div className="grid min-w-0 gap-x-12 gap-y-10 lg:grid-cols-2 2xl:grid-cols-3">
        {groups.map((group) => (
          <Group key={group.key} group={group} mark={groupMark(group.key, snapshot)} />
        ))}
      </div>
      <Withheld />
    </Section>
  )
}

/** What never leaves, as a list rather than a sentence, so each thing can be found by eye. */
function Withheld() {
  const heading = useId()
  return (
    <div className="mt-4 min-w-0 space-y-3">
      <p id={heading} className="eyebrow">
        Never sent
      </p>
      <ul
        aria-labelledby={heading}
        className="grid min-w-0 gap-x-8 gap-y-2 text-body text-muted-foreground sm:grid-cols-3 xl:grid-cols-5"
      >
        {NEVER_SENT.map((item) => (
          <li key={item} className="flex min-w-0 items-center gap-2">
            <Slash aria-hidden className="size-3.5 shrink-0 text-muted-foreground/60" />
            <span className="min-w-0 truncate">{item}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function Group({ group, mark }: { group: SharedGroup; mark: Mark }) {
  const heading = useId()
  return (
    <section aria-labelledby={heading} className="min-w-0 content-start">
      <div className="flex min-w-0 items-center gap-3 border-b border-hairline pb-3">
        <MarkTile mark={mark} />
        <h3 id={heading} className="truncate text-body font-semibold">
          {group.label}
        </h3>
      </div>
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
