"use client"

import { useEffect, useRef } from "react"
import { External, Slash } from "@/components/icons"
import { scopeName, siteHost } from "@/lib/account-link"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLinkStatus } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { useAccountLink } from "@/hooks/use-account-link"
import { LinkPanel } from "@/components/account-link/link-panel"
import { Policy } from "@/components/account-link/policy"
import { SharedFields } from "@/components/account-link/shared-fields"
import { Keys, LinkActions, StatusPanel } from "@/components/account-link/status-panel"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * Linking this server to a just-dashboard.com account: choose what the site may see and do,
 * approve the request there, and from then on read what is shared and change or end the link.
 *
 * It reports and then offers one decision, so it takes the passes of design-system §15 with the
 * host Overview as its reference, like Configuration beside it: an identity line first, whose far
 * end is the link's state and the two things done to a link that exists; then, once linked, three
 * readings of the link; no frame anywhere. The decision is the scope, a choice between three
 * things, so those are the lit cards.
 *
 * Three states share the page — not linked, waiting for approval, linked — and a fourth, linking
 * switched off, which explains the switch and offers nothing. The backend decides all four; the
 * page only asks, every five seconds while a request waits and every fifteen otherwise, so
 * approving in the other tab flips this one to linked.
 */
export default function AccountLinkPage() {
  const account = useAccountLink()
  const { status } = account

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
    return <PageState eyebrow="Settings" title="just-dashboard.com" error={account.error} />
  }

  const site = siteHost(status.siteUrl)
  const link = status.state === "linked" ? status.link : undefined

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Settings" title="just-dashboard.com" />

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
      ) : link ? (
        <>
          <StatusPanel link={link} site={site} onChangeScope={account.changeScope} />
          <SharedFields
            scope={link.scope}
            snapshot={link.lastSnapshot}
            sentAt={link.lastHeartbeatAt}
          />
          <div className="grid min-w-0 gap-x-12 gap-y-8 xl:grid-cols-2">
            <Policy policy={link.updatePolicy} scope={link.scope} site={site} />
            <Keys fingerprint={status.fingerprint} siteKeys={link.siteKeys} />
          </div>
        </>
      ) : (
        <>
          {status.state === "unlinked" && status.lastOutcome && (
            <Outcome outcome={status.lastOutcome} />
          )}
          <LinkPanel status={status} onBegin={account.begin} onCancel={account.cancel} />
        </>
      )}
    </Page>
  )
}

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
