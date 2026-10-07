"use client"

import { Cpu, External } from "@/components/icons"
import type { DNSView } from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { RowList, Row } from "@/components/row-list"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { KIND_PRODUCT } from "@/components/network/dns/resolvers"

/**
 * Whether anything here filters ads, and if not, the two ways to get it.
 *
 * What it finds is a DNS filter that is running — AdGuard Home or Pi-hole, in
 * a container or as a process — with whether it is answering on port 53 and
 * the port of its own web page, which is a link to it on the address this
 * browser used to reach the dashboard (it is the server's own, so it is the
 * one that works from here). The dashboard does not host a filter itself:
 * where there is none, the page says so, and offers the two real routes — an
 * upstream that filters, which is one click in the presets, and deploying one
 * of the filters as a project.
 */
export function Adblock({ adblock }: { adblock: DNSView["adblock"] }) {
  if (adblock.length > 0) {
    return (
      <RowList>
        {adblock.map((blocker) => (
          <Row
            key={`${blocker.kind}:${blocker.container ?? blocker.name}`}
            leading={<ProductLogo id={KIND_PRODUCT[blocker.kind]} size="sm" fallback={Cpu} />}
            title={blocker.name}
            subtitle={
              blocker.runsAs === "container" ? (
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
                  label={blocker.answering ? "Answering on port 53" : "Not answering on port 53"}
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
              </>
            }
          />
        ))}
      </RowList>
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
