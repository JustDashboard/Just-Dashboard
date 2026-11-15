"use client"

import { Archive, Gauge, type Icon } from "@/components/icons"
import { heartbeatStale, siteHost } from "@/lib/account-link"
import { relativeTime } from "@/lib/format"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { useNow } from "@/hooks/use-now"
import { WireHost, WireMark } from "@/components/wire/wire"
import { WireFlow } from "@/components/wire/wire-flow"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, hasProductLogo, platformProduct } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"

type Snapshot = Record<string, unknown>

const record = (value: unknown): Snapshot =>
  typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Snapshot) : {}
const number = (value: unknown) => (typeof value === "number" ? value : undefined)
const text = (value: unknown) => (typeof value === "string" && value ? value : undefined)

type Kind = {
  key: string
  label: string
  product?: string
  glyph?: Icon
  hue?: string
  detail: string
}

/**
 * The four kinds of thing the heartbeat carries, each drawn as what it is read from. Once there
 * is a heartbeat the detail is what it last said; before, it is what it would say at the scope
 * being chosen, so pressing Operate changes the words under Containers and Backups.
 */
function kinds(scope: AccountLinkScope, snapshot?: Snapshot): Kind[] {
  const server = record(snapshot?.server)
  const metrics = record(snapshot?.metrics)
  const containers = record(snapshot?.containers)
  const backups = record(snapshot?.backups)
  const os = text(server.os)
  const named = scope !== "observe"
  const counted = named ? "Counts and names" : "Counts only"
  const cpu = number(metrics.cpuPercent)
  const memory = number(metrics.memPercent)
  const running = number(containers.running)
  const jobs = number(backups.jobs)
  return [
    {
      key: "system",
      label: os ?? "System",
      product: platformProduct(os?.split(" ")[0]) ?? "linux",
      detail: text(server.version) ? `Just Dashboard ${server.version}` : "Version, OS and uptime",
    },
    {
      key: "metrics",
      label: "Readings",
      glyph: Gauge,
      hue: "var(--tag-cyan)",
      detail:
        cpu !== undefined && memory !== undefined
          ? `CPU ${Math.round(cpu)}% · memory ${Math.round(memory)}%`
          : "CPU, memory, disk and network",
    },
    {
      key: "containers",
      label: "Containers",
      product: "docker",
      detail: running !== undefined ? `${running} running` : counted,
    },
    {
      key: "backups",
      label: "Backups",
      glyph: Archive,
      hue: "var(--tag-violet)",
      detail: jobs !== undefined ? `${jobs} ${jobs === 1 ? "job" : "jobs"}` : counted,
    },
  ]
}

/**
 * The link, drawn as what it is: what this server reads, wired into the server, and out to the
 * site and the account on it — the deploy overview's way a request reaches a project, turned to
 * face the other way.
 *
 * The lines are the link's state. Linked and heard from, every line pulses, so the page is
 * visibly sending; a link that has stopped reaching the site holds its last line still in amber;
 * a request waiting for approval pulses only the server's line, since the server is asking the
 * site and sending nothing else; and a server that is not linked draws every line dashed, as the
 * Notifications picture draws a channel not added yet.
 */
export function ConnectPicture({
  status,
  scope,
}: {
  status: AccountLinkStatus
  /** The scope in force, or the one being chosen while nothing is linked. */
  scope: AccountLinkScope
}) {
  const now = useNow(1000)
  const link = status.state === "linked" ? status.link : undefined
  const pending = status.state === "pending"
  const quiet = Boolean(link?.lastError) || heartbeatStale(link?.lastHeartbeatAt, now)
  const host = siteHost(status.siteUrl)

  return (
    <LinkPicture
      label={`How this server connects to ${host}`}
      linked={Boolean(link)}
      pending={pending}
      quiet={quiet}
      scope={scope}
      snapshot={link?.lastSnapshot}
      cadence="Heartbeat every 30 s"
      peer={{
        eyebrow: host,
        title: link ? (
          <span className="block truncate">{link.owner}</span>
        ) : pending ? (
          <TextShimmer>Waiting for approval</TextShimmer>
        ) : (
          <span className="text-muted-foreground">Not linked</span>
        ),
        hint: link ? (
          link.lastError ? (
            <span className="text-warning">Not reaching the site</span>
          ) : link.lastHeartbeatAt ? (
            <span className={quiet ? "text-warning" : undefined}>
              Heartbeat {relativeTime(link.lastHeartbeatAt)}
            </span>
          ) : (
            "First heartbeat within 30 seconds"
          )
        ) : pending ? (
          "Approve the code below"
        ) : (
          "Pick a scope, then link"
        ),
      }}
    />
  )
}

/**
 * The picture itself, for either peer: what this server reads, the server, and the peer it
 * reports to — just-dashboard.com and an account on Settings → Connect, a Just Hub on
 * Settings → Hub. The snapshot is the same bytes to both, so the left half is the same drawing;
 * the caller says who the peer is and how the link to it reads.
 *
 * A `WireFlow`, as every picture through one hub is: it drew its own row on Settings' picture
 * with the lines laid by hand, and down a phone ran them through a column of every word. It
 * stacks there now as the other pictures do, each side under its heading.
 */
export function LinkPicture({
  label,
  linked,
  pending,
  quiet,
  scope,
  snapshot,
  cadence,
  peer,
}: {
  label: string
  linked: boolean
  pending: boolean
  /** Linked, but not heard from as it should be: the peer's line holds still in amber. */
  quiet: boolean
  /** The scope in force, or the one being chosen while nothing is linked. */
  scope: AccountLinkScope
  snapshot?: Snapshot
  /** The server's hint while it is linked and not on Tailscale. */
  cadence: string
  peer: { eyebrow: string; title: React.ReactNode; hint: React.ReactNode }
}) {
  const sending = linked && !quiet
  const server = record(snapshot?.server)
  const access = record(snapshot?.access)

  return (
    <div role="group" aria-label={label} className="animate-rise">
      <WireFlow
        start={{
          title: "What it reads",
          nodes: kinds(scope, snapshot).map((kind, index) => ({
            id: kind.key,
            mark: (size) => (
              <WireMark tone="logo" size={size}>
                {kind.product && hasProductLogo(kind.product) ? (
                  <ProductGlyph id={kind.product} />
                ) : (
                  kind.glyph && <kind.glyph aria-hidden style={{ color: kind.hue }} />
                )}
              </WireMark>
            ),
            title: kind.label,
            hint: (
              // A new reading arrives rather than being repainted (§11).
              <span key={kind.detail} className="block animate-rise truncate">
                {kind.detail}
              </span>
            ),
            name: kind.label,
            line: {
              state: "up",
              moving: sending,
              dashed: !linked,
              duration: 2.6,
              delay: index * 0.4,
            },
          })),
        }}
        hub={{
          mark: <WireHost size="lg" />,
          eyebrow: "This server",
          title: text(server.name) ?? "This server",
          hint:
            access.kind === "tailscale" ? (
              <span className="inline-flex items-center gap-1">
                <ProductGlyph id="tailscale" />
                over Tailscale
              </span>
            ) : (
              // The fingerprint is the identity line's; said here it would be said twice.
              linked && cadence
            ),
        }}
        end={{
          title: "Reports to",
          nodes: [
            {
              id: "peer",
              mark: (size) => (
                <WireMark tone="logo" size={size}>
                  <LogoGlyph className="text-brand" />
                </WireMark>
              ),
              eyebrow: peer.eyebrow,
              title: peer.title,
              hint: peer.hint,
              name: peer.eyebrow,
              line: {
                state: quiet ? "warning" : "up",
                moving: sending || pending,
                dashed: !linked && !pending,
                duration: pending ? 3.4 : 2.2,
                delay: sending ? 1.2 : 0,
              },
            },
          ],
        }}
      />
    </div>
  )
}
