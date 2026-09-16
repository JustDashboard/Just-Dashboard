"use client"

import {
  Bug,
  Connection,
  Crosshair,
  NetworkDevice,
  Shield,
  TerminalWindow,
  Users,
} from "@/components/icons"
import type { SecurityFinding } from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status, type DotTone } from "@/components/status-dot"
import { ExposurePanel } from "@/components/security/exposure-panel"
import { PosturePanel } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"

type Area = SecurityFinding["area"]

/**
 * Each row of the areas list: where it goes, and which findings — if any — are
 * about it.
 *
 * `areas: []` is deliberate and means *nothing is measured here*. Those pages
 * report a fact rather than a verdict, and the list says so by showing their
 * blurb instead of a status. They used to carry a green dot beside the blurb,
 * which claimed a clean bill of health nobody had checked.
 */
const AREAS: {
  areas: Area[]
  href: string
  title: string
  icon: typeof Shield
  blurb: string
}[] = [
  {
    areas: ["firewall"],
    href: "/security/firewall",
    title: "Firewall",
    icon: Shield,
    blurb: "Inbound rules and the default policy",
  },
  {
    areas: ["ssh"],
    href: "/security/ssh",
    title: "SSH",
    icon: TerminalWindow,
    blurb: "sshd's effective configuration",
  },
  {
    areas: ["intrusion"],
    href: "/security/intrusion",
    title: "Intrusion",
    icon: Bug,
    blurb: "fail2ban jails and ban activity",
  },
  {
    areas: ["ports"],
    href: "/security/connections",
    title: "Connections",
    icon: NetworkDevice,
    blurb: "Live TCP connections in and out",
  },
  {
    areas: [],
    href: "/security/logins",
    title: "Logins",
    icon: Users,
    blurb: "Who is on the host, and who has been",
  },
  {
    areas: [],
    href: "/security/network",
    title: "Network",
    icon: Connection,
    blurb: "Interfaces, routes and resolvers",
  },
  {
    areas: [],
    href: "/security/tools",
    title: "Tools",
    icon: Crosshair,
    blurb: "Twenty probes that run from this server",
  },
]

export default function SecurityOverviewPage() {
  const { posture, postureLoading, firewall, applyFix } = useSecurity()

  return (
    <Page>
      <PageHeader eyebrow="Network" title="Security" />

      {/* items-start so the shorter of the two keeps its own height. Stretched
          to a common height, whichever panel had less to say ended in a block
          of empty card — the page's most prominent feature being nothing. */}
      <div className="grid items-start gap-6 lg:grid-cols-2 [&>*]:min-w-0">
        <ExposurePanel />
        <PosturePanel posture={posture} loading={postureLoading} onFix={applyFix} />
      </div>

      {/* The section's own table of contents: a plain list, because a box
          around seven links to the pages beside this one is a box around the
          navigation. The glyphs stay — they are wayfinding, the same marks the
          reader is about to click through to in the rail. */}
      <Panel plain>
        <PanelHeader title="Areas" />
        <PanelBody flush>
          <RowList>
            {AREAS.map(({ areas, href, title, icon: Icon, blurb }) => {
              const findings = areas.length
                ? (posture?.findings.filter((f) => areas.includes(f.area)) ?? [])
                : null
              return (
                <Row
                  key={href}
                  href={href}
                  leading={
                    <span className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
                      <Icon className="size-3.5" />
                    </span>
                  }
                  title={title}
                  subtitle={blurb}
                  trailing={<AreaVerdict href={href} findings={findings} firewall={firewall} />}
                />
              )
            })}
          </RowList>
        </PanelBody>
      </Panel>
    </Page>
  )
}

/**
 * The right-hand end of an area row.
 *
 * The firewall is the one area whose headline is not a finding count — a
 * firewall with nothing wrong with it still has a backend, a switch and a
 * number of rules, and that is what somebody scanning this list wants.
 */
function AreaVerdict({
  href,
  findings,
  firewall,
}: {
  href: string
  findings: SecurityFinding[] | null
  firewall: ReturnType<typeof useSecurity>["firewall"]
}) {
  if (href === "/security/firewall" && firewall) {
    return (
      <Status
        verdict={firewall.enabled ? "ok" : "warning"}
        label={
          firewall.enabled
            ? `${firewall.backend} · ${firewall.rules.filter((r) => !r.ipv6).length} rules`
            : `${firewall.backend} · not enabled`
        }
        className="shrink-0"
      />
    )
  }
  if (!findings) return null
  if (findings.length === 0) {
    return <Status verdict="ok" label="nothing outstanding" className="shrink-0" />
  }
  const worst = findings.some((f) => f.level === "critical")
    ? "critical"
    : findings.some((f) => f.level === "warning")
      ? "warning"
      : "notice"
  return (
    <Status
      tone={LEVEL_TONE[worst]}
      label={`${findings.length} finding${findings.length === 1 ? "" : "s"}`}
      className="shrink-0"
    />
  )
}

const LEVEL_TONE: Record<SecurityFinding["level"], DotTone> = {
  critical: "danger",
  warning: "warning",
  notice: "notice",
}
