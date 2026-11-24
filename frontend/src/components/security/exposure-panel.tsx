"use client"

import { Fact } from "@/components/fact-line"
import { Connection, Globe, Router, type Icon } from "@/components/icons"
import type { Exposure } from "@/lib/types"
import { IdentityHeader } from "@/components/identity-header"
import { Bone } from "@/components/bone"
import { NetworkFact } from "@/components/client-mark"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Skeleton } from "@/components/ui/skeleton"

export const EXPOSURE_GRADE: Record<Exposure["grade"], { label: string; verdict: Verdict }> = {
  tailscale: { label: "Tailscale only", verdict: "ok" },
  tunnel: { label: "SSH tunnel only", verdict: "ok" },
  private: { label: "Private network", verdict: "ok" },
  public: { label: "Public addresses", verdict: "warning" },
  open: { label: "Open to the internet", verdict: "critical" },
}

/**
 * The mark for how the panel is reached, where it is no product: a tunnel is
 * a connection, a private network a router, and the two internet grades the
 * globe. The tailnet is Tailscale's own mark.
 */
const GRADE_GLYPH: Record<Exposure["grade"], Icon> = {
  tailscale: Connection,
  tunnel: Connection,
  private: Router,
  public: Globe,
  open: Globe,
}

/**
 * How this dashboard is reachable — the security property the whole product
 * rests on, and the one that lives in an env file nobody opens again after
 * install day. On screen it stays true: a machine that quietly became
 * reachable from the internet says so here instead of waiting to be found.
 *
 * It is the section's identity line, the shape the host Overview gives the
 * machine and the account pages give a session: the way in drawn as itself
 * (Tailscale's mark on a tailnet-only panel), the grade as the title in the
 * colour of its verdict, and what it was read from as the facts after it —
 * the allowed ranges, the interfaces, the tailnet address, and the address
 * this browser arrived from, which is the one every lockout guard on these
 * pages compares against. The right end is the page's other verdict, the
 * posture's, so the two answers the page is opened for share one line.
 */
export function ExposureIdentity({
  exposure,
  aside,
}: {
  exposure: Exposure | undefined
  aside?: React.ReactNode
}) {
  // Waiting, it is the line itself with bones for its words: a title and four
  // facts, which wrap under the tile on a phone as the facts they stand for
  // do. It was a tile beside two bones on one row at every width, a line of
  // facts shorter than the loaded line at 390px, so what is under it moved
  // down when the exposure landed.
  if (!exposure) {
    return (
      <IdentityHeader
        logo={<Skeleton className="size-12 rounded-xl" />}
        title={<Bone className="w-36" />}
        // As wide as the facts of a tailnet-only panel, the grade the install
        // sets up, so on a phone they wrap to the three lines those facts do.
        facts={
          <>
            <Bone className="w-40" />
            {/* The interfaces are tags, a tag's padding taller than the line's words. */}
            <Bone className="h-[18.5px] w-28" />
            <Bone className="w-40" />
            <Bone className="w-52" />
          </>
        }
        aside={aside}
      />
    )
  }
  const grade = EXPOSURE_GRADE[exposure.grade]
  return (
    <IdentityHeader
      mark={exposure.grade === "tailscale" ? "tailscale" : undefined}
      fallback={GRADE_GLYPH[exposure.grade]}
      title={
        // A flex box of the name's own 24px line: inline, the grade's 22px
        // line stood on the name's baseline and made the line 26px, two more
        // than the bone it waits as.
        <span className="flex h-6 min-w-0 items-center gap-2.5">
          <Status verdict={grade.verdict} label={grade.label} className="text-title" />
        </span>
      }
      facts={
        <>
          <Fact>
            <span className="inline-flex min-w-0 flex-wrap items-center gap-1 align-top">
              <span>allowed</span>
              {exposure.allowlist.length === 0 ? (
                <span className="font-medium text-warning">every address</span>
              ) : (
                exposure.allowlist.map((cidr) => <Cidr key={cidr} cidr={cidr} />)
              )}
            </span>
          </Fact>
          {exposure.interfaces.length > 0 && (
            <Fact>
              <span className="inline-flex min-w-0 flex-wrap items-center gap-1 align-top">
                <span>via</span>
                {exposure.interfaces.map((name) => (
                  <Tag key={name} mono>
                    {name}
                  </Tag>
                ))}
              </span>
            </Fact>
          )}
          {exposure.tailscaleIp && (
            <Fact>
              <span className="inline-flex items-center gap-1 align-top">
                <span>this host</span>
                <span className="font-mono text-foreground" title="This host's tailnet address">
                  {exposure.tailscaleIp}
                </span>
              </span>
            </Fact>
          )}
          {exposure.client && (
            <Fact>
              <span className="inline-flex min-w-0 items-center gap-1 align-top">
                <span>you</span>
                <NetworkFact
                  ip={exposure.client}
                  address={
                    <span
                      className="font-mono text-foreground"
                      title="The address this browser is reaching the dashboard from"
                    >
                      {exposure.client}
                    </span>
                  }
                />
              </span>
            </Fact>
          )}
        </>
      }
      aside={aside}
    />
  )
}

/**
 * An allowed range as the Configuration page's allowlist draws one: the
 * address in the line's own ink and the prefix in the port hue, so a /0 is
 * seen before it is read.
 */
function Cidr({ cidr }: { cidr: string }) {
  const [address, prefix] = cidr.split("/")
  return (
    <span className="numeric font-mono text-foreground">
      {address}
      {prefix !== undefined && <span className="text-tag-pink">/{prefix}</span>}
    </span>
  )
}
