"use client"

import { External, SignIn } from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import type { HeadscaleView, TailscaleView } from "@/lib/types"
import { ChoiceList } from "@/components/flow"
import { InfoTip } from "@/components/form"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { InstallHandoff, usePackageKnown } from "@/components/network/install"
import { keyExpiry, keyExpiryTip } from "./record-logic"
import { MachineRow, TailnetMachines } from "./tailnet-machines"
import { TailscaleOffers } from "./tailscale-offers"
import { tailnetState } from "./tunnel-reading"

const TAILSCALE_PITCH =
  "Tailscale joins this server to a private network of your devices without opening a port."

/**
 * Where Tailscale is not installed: the install in place where this server's
 * package manager has it, as WireGuard's is one section up, and where to get
 * it where it does not — Debian and Ubuntu carry it only once Tailscale's own
 * repository is added. The block keeps one height while the package manager
 * is asked: the sentence stands, and its button's place is held.
 */
export function TailscaleMissing({ onInstalled }: { onInstalled: () => void }) {
  const known = usePackageKnown("tailscale")
  if (known === "known") {
    return (
      <InstallHandoff
        pkg="tailscale"
        products={["tailscale", "headscale"]}
        title="Tailscale is not installed"
        description={`${TAILSCALE_PITCH} Once it is in, sign in once; its peers, exit node and subnet routes appear here.`}
        onInstalled={onInstalled}
        phases={false}
      />
    )
  }
  return (
    <EmptyState
      title="Tailscale is not installed"
      mark={<ProductLogos ids={["tailscale", "headscale"]} />}
      description={
        known === "unknown"
          ? `${TAILSCALE_PITCH} This server's package manager does not carry it: add Tailscale's repository, install it, then sign in once; its peers, exit node and subnet routes appear here.`
          : `${TAILSCALE_PITCH} Install it, then sign in once; its peers, exit node and subnet routes appear here.`
      }
      action={
        // Held in place, unseen, until the package manager has answered:
        // the block keeps its height whichever way the answer goes.
        <Button
          size="sm"
          variant="outline"
          asChild
          className={known === "asking" ? "invisible" : undefined}
        >
          <a
            href="https://tailscale.com/download/linux"
            target="_blank"
            rel="noreferrer"
            aria-hidden={known === "asking" || undefined}
            tabIndex={known === "asking" ? -1 : undefined}
          >
            <External className="size-4" />
            tailscale.com/download
          </a>
        </Button>
      }
    />
  )
}

/**
 * This server on its tailnet. It opens as the tunnel above it does: Tailscale's
 * mark, the name this server answers to there, who is online, then its
 * addresses, the tailnet, the control server, the version and when its key
 * expires. A server still waiting to be signed in says so in words and offers
 * the address it is waiting at as the line's command.
 *
 * Under the line: what Tailscale itself reports, what this server offers the
 * tailnet (`TailscaleOffers`), every other machine with how it is reached
 * (`TailnetMachines`), and a Headscale this server runs.
 */
export function TailscaleBlock({
  tailscale,
  headscale,
  onChanged,
}: {
  tailscale: TailscaleView
  headscale: HeadscaleView
  onChanged: () => void
}) {
  const self = tailscale.self
  const state = tailnetState(tailscale)
  const reports = [...tailscale.warnings, ...tailscale.health]
  const hosted = tailscale.controlServer === "tailscale"
  const verbs: Verb[] = hosted
    ? [
        {
          key: "console",
          label: "Tailscale admin console",
          icon: External,
          run: () =>
            window.open("https://login.tailscale.com/admin/machines", "_blank", "noreferrer"),
        },
      ]
    : []

  return (
    <section aria-label="Tailscale" className="flex min-w-0 flex-col gap-8">
      <HostIdentity
        label="About this server on the tailnet"
        slot="tailnet-identity"
        mark="tailscale"
        title={
          <span className="truncate">
            {self ? self.dnsName.replace(/\.$/, "") || self.hostName : "Tailscale"}
          </span>
        }
        state={<Status tone={state.tone} label={state.label} live={state.live} />}
        facts={
          <>
            <HostFact>
              <span className="block truncate font-mono">
                {self?.tailscaleIps.join(", ") || "no address"}
              </span>
            </HostFact>
            {tailscale.tailnet && (
              <>
                <FactDot />
                <HostFact>{tailscale.tailnet}</HostFact>
              </>
            )}
            <FactDot />
            <HostFact product={hosted ? "tailscale" : "headscale"}>
              {hosted ? "Tailscale's coordination" : tailscale.controlUrl}
            </HostFact>
            {tailscale.version && (
              <>
                <FactDot />
                <HostFact>v{tailscale.version.split("-")[0]}</HostFact>
              </>
            )}
            {self?.keyExpiry ? (
              <>
                <FactDot />
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <HostFact>{keyExpiry(self.keyExpiry).label}</HostFact>
                  {keyExpiryTip(tailscale) && (
                    <InfoTip label="Disable key expiry for this machine">
                      Tailscale gives every new machine a key that expires, 180 days later by
                      default. When this server&apos;s does, the server drops off the tailnet, and a
                      dashboard or Just Hub reached through it goes with it. Turn off key expiry for
                      it {hosted ? "in the Tailscale admin console" : "on the control server"}, and
                      it never will. This page warns two weeks before it expires.
                    </InfoTip>
                  )}
                </span>
              </>
            ) : null}
          </>
        }
        command={
          tailscale.authUrl ? (
            <Button size="sm" asChild>
              <a href={tailscale.authUrl} target="_blank" rel="noreferrer">
                <SignIn aria-hidden className="size-3.5" />
                Sign in
              </a>
            </Button>
          ) : undefined
        }
        menu={verbs.length > 0 && <VerbMenu verbs={verbs} label="Tailscale actions" />}
      />

      {reports.length > 0 && (
        <Notice
          title="Tailscale reports"
          tone={tailscale.warnings.some((w) => /key expire/.test(w)) ? "warning" : "default"}
        >
          <ul className="space-y-1" aria-label="Tailscale health">
            {reports.map((report) => (
              <li key={report}>{report}</li>
            ))}
          </ul>
        </Notice>
      )}

      {/* Alone in its box, so the form takes no section padding among the page's blocks. */}
      <div className="min-w-0">
        <TailscaleOffers tailscale={tailscale} onChanged={onChanged} />
      </div>
      {/* A daemon that is not running knows no machine; an empty list would say the tailnet has none. */}
      {(tailscale.running || tailscale.peers.length > 0) && (
        <TailnetMachines machines={tailscale.peers} controlServer={tailscale.controlServer} />
      )}
      {(headscale.installed || headscale.container) && <Headscale headscale={headscale} />}
    </section>
  )
}

/**
 * A Headscale this server runs, as a binary or in a container, read and never
 * changed: where it runs, its users, and its nodes as cards with their key
 * expiry and each node's routes as advertised against approved. Enrolment,
 * keys, approval and policy stay on the control server.
 */
function Headscale({ headscale }: { headscale: HeadscaleView }) {
  const online = headscale.nodes.filter((n) => n.online).length
  return (
    <section aria-label="Headscale" className="flex min-w-0 flex-col gap-4">
      <HostIdentity
        slot="headscale-identity"
        mark="headscale"
        title="Headscale"
        state={
          headscale.error ? (
            <Status tone="warning" label="Not read" />
          ) : (
            <Status
              tone={online > 0 ? "running" : "stopped"}
              label={`${online} of ${headscale.nodes.length} online`}
            />
          )
        }
        facts={
          <>
            <HostFact>
              {headscale.container ? `container ${headscale.container}` : "on this server"} ·{" "}
              {plural(headscale.nodes.length, "node")} · {plural(headscale.users.length, "user")}
            </HostFact>
            {headscale.users.length > 0 && (
              <>
                <FactDot />
                <span aria-label="Headscale users" className="min-w-0 truncate">
                  {headscale.users.map((u) => `${u.name} (${u.nodes})`).join(" · ")}
                </span>
              </>
            )}
          </>
        }
      />
      {headscale.error && <Notice title="Headscale could not be read">{headscale.error}</Notice>}
      {headscale.nodes.length > 0 && (
        <ChoiceList aria-label="Headscale nodes">
          {headscale.nodes.map((n) => {
            const expiry = n.expiry ? keyExpiry(n.expiry) : undefined
            const expired = Boolean(expiry?.expired)
            const approved = n.routesKnown ? (n.approvedRoutes ?? []) : []
            const pending = n.routesKnown
              ? (n.availableRoutes ?? []).filter((r) => !(n.approvedRoutes ?? []).includes(r))
              : []
            return (
              <MachineRow
                key={n.id}
                logo={<ProductLogo id="headscale" size="sm" />}
                name={n.givenName || n.name}
                line={
                  <>
                    <span className="font-mono">{n.ipAddresses[0] ?? "no address"}</span>
                    {[n.user, expiry?.label].filter(Boolean).map((word) => ` · ${word}`)}
                  </>
                }
                readings={[
                  {
                    key: "routes",
                    className: "w-48",
                    node: approved.length ? (
                      <span>
                        routes <span className="font-mono">{approved.join(", ")}</span>
                      </span>
                    ) : (
                      <span className="text-muted-foreground">no routes</span>
                    ),
                  },
                  {
                    key: "pending",
                    className: "w-56",
                    node: pending.length ? (
                      <span className="text-warning">
                        awaiting approval <span className="font-mono">{pending.join(", ")}</span>
                      </span>
                    ) : (
                      <span className="text-muted-foreground">nothing awaiting approval</span>
                    ),
                  },
                ]}
                narrow={
                  // What waits on somebody comes first, where the line may be cut.
                  <span className="min-w-0 truncate">
                    {pending.length > 0 && (
                      <span className="text-warning">
                        awaiting approval <span className="font-mono">{pending.join(", ")}</span>
                      </span>
                    )}
                    {pending.length > 0 && approved.length > 0 && " · "}
                    {approved.length > 0 && (
                      <>
                        routes <span className="font-mono">{approved.join(", ")}</span>
                      </>
                    )}
                    {pending.length + approved.length === 0 && (
                      <span className="text-muted-foreground">no routes</span>
                    )}
                  </span>
                }
                state={
                  <Status
                    tone={expired ? "danger" : n.online ? "running" : "stopped"}
                    label={
                      expired
                        ? "Key expired"
                        : n.online
                          ? "Online"
                          : n.lastSeen
                            ? `Seen ${relativeTime(new Date(n.lastSeen * 1000).toISOString())}`
                            : "Offline"
                    }
                  />
                }
              />
            )
          })}
        </ChoiceList>
      )}
    </section>
  )
}
