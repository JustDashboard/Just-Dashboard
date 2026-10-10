"use client"

import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { readDNSHandoffs, type DNSServiceHandoff } from "@/lib/network-dns-dhcp"
import { DNS_SERVICE_BASE } from "@/lib/network-dns-services"
import { Cpu, External } from "@/components/icons"
import type { DNSView } from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { RowList, Row } from "@/components/row-list"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { KIND_PRODUCT } from "@/components/network/dns/resolvers"
import type { DNSServiceAsk } from "@/components/network/dns/services"

type Blocker = DNSView["adblock"][number]
type Handoff = Omit<DNSServiceHandoff, "detected">

/**
 * Whether anything here filters ads, and if not, the two ways to get it.
 *
 * What it finds is a DNS filter that is running — AdGuard Home, Pi-hole or
 * Technitium, in a container or as a process — with whether it is answering on
 * port 53 and the port of its own web page, which is a link to it on the
 * address this browser used to reach the dashboard. For an administrator each
 * one is joined to the native DNS connections: the connection that already
 * reaches it opens its queries, filters, clients and DHCP; where there is none,
 * Connect opens the connection form with the engine and loopback origin filled
 * in. The dashboard does not host a filter itself: where there is none, the
 * page offers an upstream that filters and deploying one as a project.
 */
export function Adblock({
  adblock,
  onRequest,
}: {
  adblock: DNSView["adblock"]
  onRequest?: (request: DNSServiceAsk) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin") && Boolean(onRequest)
  const handoffs = usePoll(
    async (signal) => readDNSHandoffs(await get(`${DNS_SERVICE_BASE}/handoffs`, undefined, signal)),
    30_000,
    [],
    { enabled: admin && adblock.length > 0 },
  )
  const handoffFor = (blocker: Blocker): Handoff | undefined =>
    handoffs.data?.find(
      (h) =>
        h.detected.kind === blocker.kind &&
        h.detected.runsAs === blocker.runsAs &&
        (h.detected.container ?? "") === (blocker.container ?? ""),
    )
  if (adblock.length > 0) {
    return (
      <div className="flex min-w-0 flex-col gap-2">
        <RowList>
          {adblock.map((blocker) => {
            const handoff = admin ? handoffFor(blocker) : undefined
            return (
              <Row
                key={`${blocker.kind}:${blocker.container ?? blocker.name}`}
                leading={<ProductLogo id={KIND_PRODUCT[blocker.kind]} size="sm" fallback={Cpu} />}
                title={blocker.name}
                subtitle={
                  handoff?.connections[0] ? (
                    <>
                      connected as {handoff.connections[0].name} ·{" "}
                      {handoff.connections[0].management ? "reviewed changes" : "read-only"}
                    </>
                  ) : handoff && !handoff.endpoint && handoff.endpointProblem ? (
                    handoff.endpointProblem
                  ) : blocker.runsAs === "container" ? (
                    <>
                      runs as a container
                      {blocker.image && (
                        <>
                          {" · "}
                          <span className="font-mono">{blocker.image}</span>
                        </>
                      )}
                    </>
                  ) : (
                    "runs as a process on this server"
                  )
                }
                trailing={
                  <>
                    <Status
                      tone={blocker.answering ? "running" : "warning"}
                      label={
                        blocker.answering ? "Answering on port 53" : "Not answering on port 53"
                      }
                    />
                    {blocker.webPort ? (
                      <Button asChild size="xs" variant="outline">
                        <a
                          href={`http://${window.location.hostname}:${blocker.webPort}`}
                          target="_blank"
                          rel="noreferrer"
                          aria-label={`Open ${blocker.name}'s web page on port ${blocker.webPort}`}
                        >
                          Open :{blocker.webPort}
                          <External aria-hidden />
                        </a>
                      </Button>
                    ) : null}
                    {handoff?.connections[0] ? (
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Inspect ${blocker.name} through ${handoff.connections[0].name}`}
                        onClick={() =>
                          onRequest?.({
                            kind: "connection",
                            id: handoff.connections[0].id,
                            name: handoff.connections[0].name,
                          })
                        }
                      >
                        Inspect
                      </Button>
                    ) : handoff?.endpoint ? (
                      <Button
                        size="xs"
                        aria-label={`Connect ${blocker.name} at ${handoff.endpoint}`}
                        onClick={() =>
                          onRequest?.({
                            kind: "connect",
                            seed: {
                              engine: handoff.engine,
                              endpoint: handoff.endpoint ?? "",
                              name: blocker.container
                                ? `${blocker.name} (${blocker.container})`
                                : blocker.name,
                            },
                          })
                        }
                      >
                        Connect
                      </Button>
                    ) : null}
                  </>
                }
              />
            )
          })}
        </RowList>
        {admin && handoffs.error && (
          <p className="text-hint text-muted-foreground">
            The native connection handoff could not be read: {handoffs.error.message}
          </p>
        )}
      </div>
    )
  }
  return (
    <ChoiceList>
      <ChoiceRow
        index={0}
        leading={<ProductLogos ids={["adguard", "mullvad"]} />}
        title="Use an ad-blocking upstream"
        verb="Show the ad-blocking resolvers"
        description="AdGuard DNS and Mullvad drop ad and tracker lookups for every program on this server. Nothing runs here."
        onSelect={() =>
          document.getElementById("dns-upstreams")?.scrollIntoView({ behavior: "smooth" })
        }
      />
      <ChoiceRow
        index={1}
        leading={<ProductLogos ids={["adguard", "pihole"]} />}
        title="Run AdGuard Home or Pi-hole"
        verb="Deploy a DNS filter"
        description="Just Dashboard does not host one itself; it deploys the one you pick as a project, and it shows up here once it answers on port 53."
        href="/deploy/new"
      />
    </ChoiceList>
  )
}
