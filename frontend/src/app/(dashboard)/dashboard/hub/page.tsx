"use client"

import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { External, Slash, Warning } from "@/components/icons"
import { scopeName } from "@/lib/account-link"
import { relativeTime } from "@/lib/format"
import {
  atTestOrigin,
  connectTarget,
  hubHost,
  hubLabel,
  idleReason,
  isIdle,
  linkedByWords,
  linkReading,
  openable,
  sentence,
  serverCount,
} from "@/lib/hub-link"
import { notify } from "@/lib/toast"
import type { HubLinkScope, HubLinkStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useHubLink, useShellHub } from "@/hooks/use-hub-link"
import { Policy } from "@/components/account-link/policy"
import { SharedFields } from "@/components/account-link/shared-fields"
import { Keys, ScopeSection } from "@/components/account-link/status-panel"
import { ConnectPanel, MachineFact, WaitingPanel } from "@/components/hub-link/connect-panel"
import { HubPicture } from "@/components/hub-link/hub-picture"
import { SignInPanel } from "@/components/hub-link/sign-in-panel"
import { HubActivity, HubLinkActions, HubNotices } from "@/components/hub-link/status-panel"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * Settings → Hub: this server's link to a Just Hub — connect it, see whose machine the hub is,
 * give the hub the code to type in, and from then on read what it reports, what the hub has run
 * here, change the scope or disconnect. The contract is docs/internal/backend/hub-link-protocol.md
 * §8.
 *
 * It is Settings → Connect's page pointed at another peer, and in the same register (§16): it
 * reports a link's state and offers one decision about it. The hub's Connect button lands here
 * with `?connect=<hub host>`, which is a sequence with an outcome — but the sequence is one
 * section of a page that reads the link before and after it, as on Connect, so the page is not a
 * flow page and the connect form is not a `FlowPanel`. Its passes are §15's with Connect as the
 * reference: an identity line whose far end is the link's state and the two things done to a link
 * that exists; the link drawn as what it is (`hub-picture.tsx`, Connect's picture with the hub at
 * its end); then one section at a time under hairlines. §15 pass 2 is dropped as Connect drops it.
 *
 * Nothing connects by itself. `?connect=` only fills the address in and says so, no scope is
 * chosen for the person (what each scope sees and can do is beside it), and a server
 * administrator picks one and presses Connect (§8.1). Typed in, the address starts at the
 * protocol's default scope, `maintain`. Unlinked servers do not draw this page in the rail (`hubLink` in
 * `nav.ts`), so the hub's button, the palette and the address bar are the ways in.
 *
 * A link this server has stopped using — its address fails the URL rule now, or another machine
 * answers at the hub's name (§2.2, §2.4) — is still stored, and reads as **Idle** at the top of
 * the page, with the reason for an administrator: nothing is sent and no sync is offered until it
 * is disconnected, which then tells no one. Nor is the hub's address a link any more, in the title
 * or under a notice: after "the hub's machine changed" it answers on a machine this server
 * refused. The hub's name, its notices and the servers it lists are its own words, so each is said
 * as the hub's, "Just Hub (hub)" (§8.10). A link the hub removed is gone from here at once, and
 * the page says so once (§8.9).
 *
 * Without `system.admin` the status has no `admin` part, and the page says what it can: whether
 * and to which hub this server is connected, and what the hub is saying.
 *
 * Once linked, every account also finds "Sign in from Just Hub" here (`sign-in-panel.tsx`): its
 * own choice to let a hub user open this server signed in as it (§9), which is off unless the
 * operator, the hub and the account each turn it on.
 */
export default function HubLinkPage() {
  const hub = useHubLink()
  const { status } = hub
  const { refresh: refreshAuth } = useAuth()
  const shell = useShellHub()
  const params = useSearchParams()
  // Null follows the link the person arrived by; typing takes over from it.
  const [typed, setTyped] = useState<string | null>(null)
  // A link someone else wrote leaves the scope for the person to choose (§8.1).
  const [choice, setChoice] = useState<HubLinkScope | undefined>(() =>
    params.get("connect") ? undefined : "maintain",
  )

  // Approval happens in the hub's tab, so arriving at "linked" is news this one announces — and
  // the rail, the palette and the tab's title follow the session's `hub` field, which was read
  // when the page loaded, so it is read again whenever the link starts or stops.
  const state = status?.state
  const name = status?.hubName
  const previous = useRef(state)
  useEffect(() => {
    const was = previous.current
    previous.current = state
    if (was === undefined || was === state) return
    if (was === "pending" && state === "linked") notify.success(`Connected to ${hubLabel(name)}`)
    if ((was === "linked") !== (state === "linked")) {
      void refreshAuth().catch(() => undefined)
      shell?.refresh()
    }
  }, [state, name, refreshAuth, shell])

  if (!status) {
    return <PageState eyebrow="Settings" title="Hub" error={hub.error} />
  }

  const admin = status.admin
  const linked = status.state === "linked"
  const pending = status.state === "pending"
  const link = linked ? admin?.link : undefined
  const request = pending ? admin?.request : undefined
  const url = status.hubUrl ?? request?.hubUrl
  const host = hubHost(url)
  const prefill = connectTarget(params.get("connect"), admin?.testOrigin)
  const invited = prefill ? hubHost(prefill) : undefined
  const address = typed ?? prefill
  const scope = status.scope ?? request?.scope ?? choice
  const notices = status.notices ?? []
  const reading = linkReading(status)
  const hubLink = openable(url)
  // Every reader learns the link is idle; only an administrator why.
  const stopped = isIdle(status)
  const idle = idleReason(status)
  const testOrigin = atTestOrigin(url, admin?.testOrigin)

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Settings" title="Hub" />

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
          // Only a hub this server is connected to, or asking to be, is a destination; an idle
          // link's address may now answer on a machine this server refused.
          hubLink && (pending || (linked && !stopped)) ? (
            <a
              href={hubLink}
              target="_blank"
              rel="noopener noreferrer"
              title={hubLink}
              className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:[&_svg]:text-foreground"
            >
              <span className="truncate">{host}</span>
              <External aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
            </a>
          ) : linked && host ? (
            // Still the hub this server is linked to, said as text.
            host
          ) : (
            "Just Hub"
          )
        }
        facts={<Facts status={status} />}
        aside={
          <span className="flex flex-wrap items-center gap-3">
            <Status
              tone={reading.tone}
              label={pending ? <TextShimmer>{reading.label}</TextShimmer> : reading.label}
            />
            {linked && admin && (
              <HubLinkActions
                host={host}
                name={status.hubName ? hubLabel(status.hubName) : host}
                idle={stopped}
                onSync={hub.sync}
                onDisconnect={hub.disconnect}
              />
            )}
          </span>
        }
      />

      {!status.enabled ? (
        <Notice icon={Slash} title="Connecting to a hub is turned off">
          <code className="font-mono">JD_HUB_LINK=false</code> is set in this server&apos;s{" "}
          <code className="font-mono">.env</code>, so it never contacts a hub, and a link it had
          stays stored and idle. Remove it, or set it to <code className="font-mono">true</code>,
          and restart the dashboard to connect this server to Just Hub.
        </Notice>
      ) : (
        <>
          {admin?.testOrigin && (
            <Notice tone="warning" icon={Warning} title="A test hub address is allowed">
              <code className="font-mono">JD_HUB_LINK_TEST_ORIGIN</code> is set to{" "}
              <code className="font-mono">{admin.testOrigin}</code>. It is for tests and must never
              be used in production.
            </Notice>
          )}
          {stopped && (
            <div data-slot="hub-link-idle">
              <Notice tone="danger" icon={Warning} title="The link is idle">
                {idle && <p className="break-words">{sentence(idle)}</p>}
                <p className={cn(idle && "mt-1")}>
                  Nothing is sent to {host} and nothing it asks is run until{" "}
                  {admin
                    ? "you disconnect and connect again."
                    : "an administrator disconnects it and connects again."}
                </p>
              </Notice>
            </div>
          )}
          {invited && linked && invited !== host && (
            <Notice title={`Already connected to ${host}`}>
              A server reports to one hub. To connect it to {invited}, disconnect it from {host}{" "}
              first.
            </Notice>
          )}
          {status.state === "unlinked" && admin?.lastOutcome && (
            <Outcome outcome={admin.lastOutcome} />
          )}
          {!admin && !linked && (
            <Notice title="Only an administrator can connect this server">
              Connecting a server to a hub, and choosing what the hub may see and do, needs the
              administrator role on this server.
            </Notice>
          )}
          {/* Before a scope is chosen the drawing reads as the least the hub could see. */}
          <HubPicture status={status} scope={scope ?? "observe"} address={address} />
          <div className="flex min-w-0 flex-col divide-y divide-hairline">
            {linked ? (
              <>
                {link && (
                  <HubActivity
                    status={status}
                    link={link}
                    host={host}
                    idle={stopped}
                    className={BLOCK}
                  />
                )}
                {notices.length > 0 && (
                  <HubNotices
                    notices={notices}
                    hubUrl={stopped ? undefined : url}
                    hubName={status.hubName}
                    className={BLOCK}
                  />
                )}
                {admin && status.scope && (
                  <ScopeSection scope={status.scope} onChange={hub.changeScope} className={BLOCK} />
                )}
                <SignInPanel
                  status={status}
                  hubName={status.hubName ? hubLabel(status.hubName) : host}
                  idle={Boolean(idle)}
                  onAllow={hub.allowSignIn}
                  onRevoke={hub.revokeSignIn}
                  onRemove={hub.removeGrant}
                  className={BLOCK}
                />
                {link && (
                  <>
                    <SharedFields
                      scope={status.scope ?? scope ?? "observe"}
                      snapshot={link.lastSnapshot}
                      sentAt={status.lastHeartbeatAt}
                      className={BLOCK}
                    />
                    <div className={cn("grid min-w-0 gap-x-12 gap-y-8 xl:grid-cols-2", BLOCK)}>
                      <Policy
                        policy={link.updatePolicy}
                        scope={status.scope ?? scope ?? "observe"}
                        site={host}
                        peer="hub"
                      />
                      <div className="flex min-w-0 flex-col gap-3">
                        <Keys
                          fingerprint={admin?.fingerprint}
                          siteKeys={link.hubKeys}
                          keyLabel="Hub key"
                        />
                        <p className="text-xs text-muted-foreground">
                          Compare the hub key&apos;s fingerprint with the key fingerprint on the
                          hub&apos;s Settings page.
                        </p>
                        <MachineFact machine={link.hubMachine} testOrigin={testOrigin} />
                      </div>
                    </div>
                  </>
                )}
              </>
            ) : admin ? (
              <>
                {request ? (
                  <WaitingPanel
                    request={request}
                    fingerprint={admin.fingerprint}
                    testOrigin={testOrigin}
                    onCancel={hub.cancel}
                    className={BLOCK}
                  />
                ) : (
                  <ConnectPanel
                    address={address}
                    onAddress={setTyped}
                    invited={invited}
                    scope={choice}
                    onScope={setChoice}
                    onBegin={hub.begin}
                    onCheck={hub.check}
                    className={BLOCK}
                  />
                )}
                {scope && <SharedFields scope={scope} className={BLOCK} />}
              </>
            ) : null}
          </div>
        </>
      )}
    </Page>
  )
}

/** Each section stands alone under a hairline, at the deploy settings pages' rhythm. */
const BLOCK = "py-8 first:pt-4 last:pb-2"

/**
 * The identity line's facts: what the link is, never its state, which the line's far end says
 * (`linkReading`) — said in both, an unlinked or waiting server read its state twice on one line,
 * and on a phone on two lines one under the other. Switched off, the notice under the line says
 * why, so the line has nothing to add. How many servers the hub holds is said once it has answered
 * a report: before that the fleet is empty because nothing is known yet, not because the hub holds
 * none.
 */
function Facts({ status }: { status: HubLinkStatus }) {
  if (!status.enabled) return null
  const link = status.state === "linked" ? status.admin?.link : undefined
  if (status.state === "linked") {
    return (
      <>
        {status.hubName && <span className="truncate">{hubLabel(status.hubName)}</span>}
        {status.scope && (
          <>
            <FactDot />
            <span>{scopeName(status.scope)} scope</span>
          </>
        )}
        {status.lastHeartbeatAt && (
          <>
            <FactDot />
            <span>{serverCount(status.fleet?.length ?? 0)}</span>
          </>
        )}
        {link && (
          <>
            <FactDot />
            <span>
              connected {relativeTime(link.linkedAt)} by {linkedByWords(link.linkedBy)}
            </span>
          </>
        )}
      </>
    )
  }
  const fingerprint = status.admin?.fingerprint
  if (!fingerprint) return null
  return (
    <span>
      this server <span className="font-mono text-foreground">{fingerprint}</span>
    </span>
  )
}

const OUTCOME = {
  denied: {
    tone: "warning",
    title: "The request was declined",
    body: "It was declined in the hub.",
  },
  expired: {
    tone: "default",
    title: "The request expired",
    body: "Nobody approved it in the hub in time.",
  },
  failed: { tone: "danger", title: "Connecting failed", body: "It ended without a link." },
  removed: {
    tone: "warning",
    title: "Removed in Just Hub",
    body: "The hub no longer lists this server, so this server forgot its link and sends the hub nothing.",
  },
} as const

/**
 * Why this server is not linked, said once: dismissing it remembers which outcome it was for this
 * tab, so the next one is news again. A declined or expired request says so in the page's own
 * words, which are all the backend's message adds; a failed one says why in the backend's — "the
 * hub's machine changed" when another machine answered at the hub's name before the approval was
 * collected (§2.4), with any of the hub's own words in it already quoted as the hub's (§8.10) — as
 * plain text. A link the hub removed (`410`, §8.9) is not a request that ended: the page says the
 * link is gone, in its own words, and when.
 */
function Outcome({
  outcome,
}: {
  outcome: NonNullable<NonNullable<HubLinkStatus["admin"]>["lastOutcome"]>
}) {
  const [dismissed, setDismissed] = useSessionState("hub-link.outcome", "")
  if (dismissed === outcome.at) return null
  const words = OUTCOME[outcome.result]
  const why =
    outcome.result === "failed" && outcome.message ? sentence(outcome.message) : words.body
  return (
    <Notice tone={words.tone} title={words.title}>
      <p className="break-words">{why}</p>
      <p className="mt-1">
        {outcome.result === "removed" ? "Removed" : "Ended"} {relativeTime(outcome.at)}.
      </p>
      <Button size="xs" variant="outline" className="mt-2" onClick={() => setDismissed(outcome.at)}>
        Dismiss
      </Button>
    </Notice>
  )
}
