"use client"

import { useEffect, useRef, useState } from "react"
import { External, Slash } from "@/components/icons"
import { scopeName, siteHost } from "@/lib/account-link"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { useAccountLink } from "@/hooks/use-account-link"
import { ConnectPicture } from "@/components/account-link/connect-picture"
import { LinkPanel } from "@/components/account-link/link-panel"
import { Policy } from "@/components/account-link/policy"
import { SharedFields } from "@/components/account-link/shared-fields"
import { Activity, Keys, LinkActions, ScopeSection } from "@/components/account-link/status-panel"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * Settings → Connect: linking this server to a just-dashboard.com account — choose what the site
 * may see and do, approve the request there, and from then on read what is shared and change or
 * end the link. It was named after the site's domain in the sidebar, which said where the link
 * goes but not what the page does; the domain is still the identity line's name.
 *
 * It reports and then offers one decision, so it takes the passes of design-system §15 with the
 * deploy overview as its reference: an identity line first, whose far end is the link's state and
 * the two things done to a link that exists; then the link drawn as what it is
 * (`connect-picture.tsx`) — what this server reads, wired through it to the site and the account
 * — whose lines pulse while heartbeats go out; then one section at a time under hairlines, the
 * deploy settings pages' rhythm, so each part is read on its own. The decision is the scope, a
 * choice between three things, so those are the lit cards.
 *
 * §15 pass 2 is dropped here, the way `/git` drops it. The link had three tiles — linked,
 * last heartbeat, last command — and the operator asked for them to go. When it was linked and by
 * whom is the identity line's; the heartbeat is the picture's site line and the Activity row with
 * its `Status`; the last command is the Activity row beside it, drawn as what it acted on.
 *
 * Three states share the page — not linked, waiting for approval, linked — and a fourth, linking
 * switched off, which explains the switch and offers nothing. The backend decides all four; the
 * page only asks, every five seconds while a request waits and every fifteen otherwise, so
 * approving in the other tab flips this one to linked.
 */
export default function AccountLinkPage() {
  const account = useAccountLink()
  const { status } = account
  // Least access first: the person who only presses Link has chosen the scope that cannot act.
  const [choice, setChoice] = useState<AccountLinkScope>("observe")

  // Approval happens in another tab, so arriving at "linked" is news the page has to announce.
  const state = status?.state
  const owner = status?.link?.owner
  const previous = useRef(state)
  useEffect(() => {
    if (previous.current === "pending" && state === "linked") {
      notify.success(`Linked to ${owner ?? "your account"}`)
    }
    previous.current = state
  }, [state, owner])

  if (!status) {
    return <PageState eyebrow="Settings" title="Connect" error={account.error} />
  }

  const site = siteHost(status.siteUrl)
  const link = status.state === "linked" ? status.link : undefined
  const scope = link?.scope ?? status.request?.scope ?? choice

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Settings" title="Connect" />

      <HostIdentity
        logo={
          <span
            aria-hidden
            className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-hairline bg-surface-sunken"
          >
            <LogoGlyph className="h-6 w-auto text-brand" />
          </span>
        }
        title={
          // With linking off nothing here leads to the site: it is a name, not a destination.
          status.enabled ? (
            <a
              href={status.siteUrl}
              target="_blank"
              rel="noopener noreferrer"
              title={status.siteUrl}
              className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:[&_svg]:text-foreground"
            >
              <span className="truncate">{site}</span>
              <External aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
            </a>
          ) : (
            site
          )
        }
        facts={<Facts status={status} />}
        aside={
          <span className="flex flex-wrap items-center gap-3">
            <LinkState status={status} />
            {link && (
              <LinkActions
                site={site}
                owner={link.owner}
                onSync={account.sync}
                onUnlink={account.unlink}
              />
            )}
          </span>
        }
      />

      {!status.enabled ? (
        <Notice icon={Slash} title="Linking is turned off">
          <code className="font-mono">JD_ACCOUNT_LINK=false</code> is set in this server&apos;s{" "}
          <code className="font-mono">.env</code>, so it never contacts {site}. Remove it, or set it
          to <code className="font-mono">true</code>, and restart the dashboard to link this server.
        </Notice>
      ) : (
        <>
          {status.state === "unlinked" && status.lastOutcome && (
            <Outcome outcome={status.lastOutcome} />
          )}
          <ConnectPicture status={status} scope={scope} />
          <div className="flex min-w-0 flex-col divide-y divide-hairline">
            {link ? (
              <>
                <Activity link={link} site={site} className={BLOCK} />
                <ScopeSection scope={link.scope} onChange={account.changeScope} className={BLOCK} />
                <SharedFields
                  scope={link.scope}
                  snapshot={link.lastSnapshot}
                  sentAt={link.lastHeartbeatAt}
                  className={BLOCK}
                />
                <div className={cn("grid min-w-0 gap-x-16 gap-y-12 xl:grid-cols-2", BLOCK)}>
                  <Policy policy={link.updatePolicy} scope={link.scope} site={site} />
                  <Keys fingerprint={status.fingerprint} siteKeys={link.siteKeys} />
                </div>
              </>
            ) : (
              <>
                <LinkPanel
                  status={status}
                  scope={choice}
                  onScope={setChoice}
                  onBegin={account.begin}
                  onCancel={account.cancel}
                  className={BLOCK}
                />
                <SharedFields scope={scope} className={BLOCK} />
              </>
            )}
          </div>
        </>
      )}
    </Page>
  )
}

/** Each section stands alone under a hairline, with room enough round it to be read as one. */
const BLOCK = "py-12 first:pt-6 last:pb-4"

function Facts({ status }: { status: AccountLinkStatus }) {
  if (!status.enabled) return <span>Turned off by JD_ACCOUNT_LINK</span>
  const link = status.state === "linked" ? status.link : undefined
  if (link) {
    return (
      <>
        <span className="truncate">{link.owner}</span>
        <FactDot />
        <span>{scopeName(link.scope)} scope</span>
        <FactDot />
        <span>
          linked {relativeTime(link.linkedAt)} by{" "}
          {link.linkedBy === "cli" ? "the terminal" : link.linkedBy}
        </span>
      </>
    )
  }
  return (
    <>
      <span>{status.state === "pending" ? "Waiting for approval" : "Not linked"}</span>
      {status.fingerprint && (
        <>
          <FactDot />
          <span>
            this server <span className="font-mono text-foreground">{status.fingerprint}</span>
          </span>
        </>
      )}
    </>
  )
}

/** The link's state, as the far end of the identity line. */
function LinkState({ status }: { status: AccountLinkStatus }) {
  if (!status.enabled) return <Status tone="stopped" label="Turned off" />
  if (status.state === "pending") {
    return <Status tone="warning" label={<TextShimmer>Waiting for approval</TextShimmer>} />
  }
  if (status.state === "linked") {
    return status.link?.lastError ? (
      <Status tone="warning" label="Linked, not reaching the site" />
    ) : (
      <Status tone="running" label="Linked" />
    )
  }
  return <Status tone="stopped" label="Not linked" />
}

const OUTCOME = {
  denied: {
    tone: "warning",
    title: "The link request was denied",
    body: "It was denied on the site.",
  },
  expired: {
    tone: "default",
    title: "The link request expired",
    body: "Nobody approved it within 15 minutes.",
  },
  failed: { tone: "danger", title: "Linking failed", body: "" },
} as const

/**
 * Why the last request ended without a link, said once: dismissing it remembers which request it
 * was for this tab, so the next one that ends the same way is news again.
 */
function Outcome({ outcome }: { outcome: NonNullable<AccountLinkStatus["lastOutcome"]> }) {
  const [dismissed, setDismissed] = useSessionState("account-link.outcome", "")
  if (dismissed === outcome.at) return null
  const words = OUTCOME[outcome.result]
  return (
    <Notice tone={words.tone} title={words.title}>
      <p>
        {[words.body, outcome.message].filter(Boolean).join(" ")} {relativeTime(outcome.at)}.
      </p>
      <Button size="xs" variant="outline" className="mt-2" onClick={() => setDismissed(outcome.at)}>
        Dismiss
      </Button>
    </Notice>
  )
}
