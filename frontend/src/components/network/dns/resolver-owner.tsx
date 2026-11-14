"use client"

import Link from "next/link"
import type { ResolverOwner } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"

const CHAIN: Record<ResolverOwner["chain"], string> = {
  stub: "systemd-resolved's local stub",
  uplink: "the upstream servers directly, from resolved's list",
  "local-cache": "a resolver on loopback",
  direct: "the servers the file lists",
  missing: "nothing: the file does not exist",
}

const CONFIDENCE: Record<ResolverOwner["confidence"], string> = {
  confirmed: "Confirmed",
  declared: "Declared by the file",
  inferred: "Inferred",
  unknown: "Unrecognised",
}

/** Where the owner's hand-off leads, as a link that says so. */
export function OwnerHandoff({ owner }: { owner: ResolverOwner }) {
  if (!owner.handoffHref) return null
  return owner.handoffHref.startsWith("/") ? (
    <Link className="underline" href={owner.handoffHref}>
      Open the interfaces
    </Link>
  ) : (
    // An anchor of the page's first view, linked whole so it also leads there
    // from the settings, where the same hand-off is said.
    <Link className="underline" href={`/network/dns${owner.handoffHref}`}>
      Per-link DNS
    </Link>
  )
}

/**
 * Who decides what /etc/resolv.conf says on this host, and so where a DNS
 * change is made. The page writes systemd-resolved's drop-in; on a host where
 * NetworkManager, resolvconf, netconfig, dhcpcd or Tailscale writes the file,
 * that drop-in reaches nothing programs ask, and this says so with the owner's
 * own place to change it. The evidence is what the verdict rests on — the
 * file's link and header, the owners' configuration, the running services —
 * and disagreements between them are drawn as a warning, not resolved.
 */
export function ResolverOwnerPanel({ owner }: { owner: ResolverOwner }) {
  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Resolver owner"
        actions={
          <Status
            tone={
              owner.confidence === "confirmed"
                ? "running"
                : owner.confidence === "unknown"
                  ? "warning"
                  : "notice"
            }
            label={CONFIDENCE[owner.confidence]}
          />
        }
      />
      <PanelBody className="flex min-w-0 flex-col gap-4">
        <DetailList aria-label="Resolver owner">
          <Detail label="Writes /etc/resolv.conf">{owner.name}</Detail>
          <Detail label="Programs ask">
            {owner.chain === "local-cache" ? owner.resolver : CHAIN[owner.chain]}
          </Detail>
          <Detail label="This page's drop-in">
            {owner.dashboardWrites
              ? "Reaches what programs ask"
              : "Does not reach what programs ask"}
          </Detail>
          <Detail label="Change DNS" className="break-words">
            {owner.handoff} <OwnerHandoff owner={owner} />
          </Detail>
        </DetailList>
        {owner.conflicts.length > 0 && (
          <Notice title="The sources disagree" tone="warning">
            <ul className="list-inside list-disc space-y-1">
              {owner.conflicts.map((conflict) => (
                <li key={conflict}>{conflict}</li>
              ))}
            </ul>
          </Notice>
        )}
        <ul aria-label="Owner evidence" className="flex min-w-0 flex-col gap-1">
          {owner.evidence.map((fact, index) => (
            <li
              key={`${fact.source}:${index}`}
              className="text-hint break-words text-muted-foreground"
            >
              <span className="font-mono">{fact.source}</span> · {fact.detail}
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}
