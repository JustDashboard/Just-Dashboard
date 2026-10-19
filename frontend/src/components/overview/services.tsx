"use client"

import { useMemo } from "react"
import {
  Box,
  GitHubMark,
  Globe,
  Puzzle,
  Servers,
  SettingsGear,
  Shield,
  TerminalWindow,
} from "@/components/icons"
import { ApiError } from "@/lib/api"
import { plural } from "@/lib/format"
import type { RdOverview } from "@/lib/remote-desktop"
import type {
  Certificate,
  Container,
  Exposure,
  GitRepo,
  SystemdList,
  UpdateReport,
} from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { StatLink, StatTile } from "@/components/stat-tile"
import type { Tone } from "@/components/tone"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import {
  ProductGlyphs,
  containerProducts,
  hostProduct,
  platformProduct,
} from "@/components/product-logo"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import { unitCounts } from "./order"

/**
 * A module that is genuinely absent on this host, as opposed to a poll that
 * failed once. The dashboard returns a precise code for the former; a dropped
 * VPN tunnel — which is a normal Tuesday here — is anything else, and must not
 * turn a card that read "12 running" into a false claim about the host.
 */
function moduleGone(error: Error | undefined): boolean {
  return (
    error instanceof ApiError &&
    (error.code === "docker_unavailable" ||
      error.code === "not_installed" ||
      error.code === "no_proxy")
  )
}

/**
 * One module, its headline figure, and the way to its page — a `StatTile`
 * behind a `StatLink`, exactly as the Docker overview draws its own run, so
 * the Services row reads as the top row does: figures on the page, hairlines
 * between them, and an arrow that says the tile goes somewhere.
 *
 * The figure is a count and the word after it is the tile's `trailing`, as
 * the readings at the top write "6.5 GB free": two of these share a phone's
 * width, and "3 certificates" at the figure's size did not fit half of one.
 *
 * The glyph before the name is wayfinding, not decoration: it is the same
 * mark the sidebar entry carries, so the eye finds "Docker" without reading.
 * The figure rises once when its poll lands, so a page of eight tiles fills in
 * rather than flickering from bone to number.
 */
function ServiceTile({
  icon: Icon,
  title,
  href,
  value,
  unit,
  hint,
  tone = "default",
  loading,
  unavailable,
  products = [],
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  href: string
  value?: React.ReactNode
  /** What the figure counts, after it at the hint's size. */
  unit?: string
  hint?: React.ReactNode
  /** What the figure counts, as the products themselves — `product-logo` ids. */
  products?: string[]
  /** Colours the figure as a reading of state, never as decoration. */
  tone?: Tone
  loading?: boolean
  unavailable?: boolean
}) {
  const settled = !loading
  const known = !loading && !unavailable && value != null
  const figure = loading ? (
    <Skeleton className="my-1.5 h-5 w-20" />
  ) : unavailable ? (
    "Not available"
  ) : value == null ? (
    "Unreachable"
  ) : (
    value
  )
  return (
    <StatLink href={href} label={title}>
      <StatTile
        className="h-full transition-colors group-hover:bg-row-hover"
        label={
          <>
            <Icon aria-hidden className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand" />
            {title}
          </>
        }
        value={
          <span
            key={settled ? "figure" : "skeleton"}
            className={cn(
              "inline-block max-w-full truncate align-bottom",
              settled && "animate-rise",
            )}
          >
            {figure}
          </span>
        }
        trailing={known ? unit : undefined}
        tone={known ? tone : "default"}
        hint={
          unavailable ? (
            "on this host"
          ) : (
            <span className="inline-flex max-w-full min-w-0 items-center gap-2">
              {hint && <span className="truncate">{hint}</span>}
              {/* Off on a phone, where two tiles share the width and the
                  marks left the words one character. */}
              {settled && (
                <span className="shrink-0 max-sm:hidden">
                  <ProductGlyphs ids={products} />
                </span>
              )}
            </span>
          )
        }
      />
    </StatLink>
  )
}

export function DockerTile({ read }: { read: PollState<Container[]> }) {
  const { data, error, loading } = read
  const running = data?.filter((c) => c.state === "running")
  const stopped = data ? data.length - (running?.length ?? 0) : 0
  return (
    <ServiceTile
      icon={Box}
      title="Docker"
      href="/docker"
      products={containerProducts(running ?? [])}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={running?.length}
      unit="running"
      hint={
        data
          ? [plural(data.length, "container"), stopped > 0 && `${stopped} stopped`]
              .filter(Boolean)
              .join(" · ")
          : undefined
      }
    />
  )
}

export function ProxyTile({ read }: { read: PollState<Certificate[]> }) {
  const { data, error, loading } = read
  const soonest = useMemo(() => {
    if (!data || data.length === 0) return undefined
    return [...data].sort((a, b) => a.daysLeft - b.daysLeft)[0]
  }, [data])
  return (
    <ServiceTile
      icon={Globe}
      title="Proxy & TLS"
      href="/proxy"
      products={data && data.length > 0 ? ["nginx-static", "lets-encrypt"] : ["nginx-static"]}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={data?.length}
      unit={data?.length === 1 ? "certificate" : "certificates"}
      tone={soonest?.expired ? "danger" : "default"}
      hint={
        soonest
          ? soonest.expired
            ? "One expired"
            : `Renews in ${soonest.daysLeft}d`
          : data
            ? "None issued"
            : undefined
      }
    />
  )
}

export function SecurityTile({ read }: { read: PollState<Exposure> }) {
  const { data, error, loading } = read
  const grade = data ? EXPOSURE_GRADE[data.grade] : undefined
  return (
    <ServiceTile
      icon={Shield}
      title="Security"
      href="/security"
      products={data?.grade === "tailscale" ? ["tailscale"] : []}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={grade?.label}
      tone={
        grade?.verdict === "critical"
          ? "danger"
          : grade?.verdict === "warning"
            ? "warning"
            : "default"
      }
      hint={
        data
          ? `${plural(data.allowlist.length, "allowed range")} · ${plural(data.interfaces.length, "interface")}`
          : undefined
      }
    />
  )
}

export function PackagesTile({
  read,
  platform,
}: {
  read: PollState<UpdateReport>
  platform: string
}) {
  const { data, error, loading } = read
  const pending = data?.packages.length ?? 0
  return (
    <ServiceTile
      icon={Puzzle}
      title="Packages"
      href="/packages"
      products={[platformProduct(platform)].filter((id): id is string => Boolean(id))}
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      value={data ? (pending === 0 ? "Up to date" : pending) : undefined}
      unit={pending > 0 ? (pending === 1 ? "update" : "updates") : undefined}
      tone={data?.rebootRequired || (data?.securityCount ?? 0) > 0 ? "warning" : "default"}
      hint={
        data?.rebootRequired
          ? "Reboot required"
          : data && data.securityCount > 0
            ? `${data.securityCount} security`
            : data && pending > 0 && data.securityFiltering
              ? "None are security updates"
              : undefined
      }
    />
  )
}

/**
 * What systemd runs, and what it gave up on: a failed unit is the one thing on
 * the machine that stopped and will not try again by itself, so while there
 * is one it is the figure, in red, rather than a word in the hint.
 */
export function UnitsTile({ read }: { read: PollState<SystemdList> }) {
  const { data, error, loading } = read
  const counts = data ? unitCounts(data.units) : undefined
  const failed = counts?.failed ?? 0
  return (
    <ServiceTile
      icon={Servers}
      title="System services"
      href="/processes/services"
      products={["systemd"]}
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      value={counts ? (failed > 0 ? failed : counts.running) : undefined}
      unit={failed > 0 ? "failed" : "running"}
      tone={failed > 0 ? "danger" : "default"}
      hint={counts ? (failed > 0 ? `${counts.running} running` : "None failed") : undefined}
    />
  )
}

/**
 * The working copies on this server, drawn as the forges they push to, and
 * what is waiting in them: uncommitted work first, because it is the one
 * thing here a deploy or a restore cannot bring back.
 */
export function GitTile({ read }: { read: PollState<{ available: boolean; repos: GitRepo[] }> }) {
  const { data, error, loading } = read
  const repos = data?.repos ?? []
  const dirty = repos.filter((repo) => repo.dirty).length
  const behind = repos.filter((repo) => repo.behind > 0).length
  const conflicted = repos.filter((repo) => repo.conflicts > 0).length
  const forges = [...new Set(repos.flatMap((repo) => hostProduct(repo.remote) ?? []))]
  return (
    <ServiceTile
      icon={GitHubMark}
      title="Git"
      href="/git"
      products={forges}
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      value={
        !data
          ? undefined
          : repos.length === 0
            ? "None yet"
            : conflicted > 0
              ? conflicted
              : repos.length
      }
      unit={
        repos.length === 0
          ? undefined
          : conflicted > 0
            ? "in conflict"
            : repos.length === 1
              ? "repository"
              : "repositories"
      }
      tone={conflicted > 0 ? "warning" : "default"}
      hint={
        !data || repos.length === 0
          ? undefined
          : [dirty > 0 ? `${dirty} uncommitted` : "All committed", behind > 0 && `${behind} behind`]
              .filter(Boolean)
              .join(" · ")
      }
    />
  )
}

const OS_PRODUCT = { windows: "windows", macos: "apple", linux: "linux" } as const

/** The computers the screen can be opened on, each drawn as its system, and which are open now. */
export function RemoteDesktopTile({ read }: { read: PollState<RdOverview> }) {
  const { data, error, loading } = read
  const computers = data?.computers ?? []
  const open = computers.filter((computer) => computer.live > 0).length
  return (
    <ServiceTile
      icon={TerminalWindow}
      title="Remote Desktop"
      href="/remote-desktop"
      products={[...new Set(computers.map((computer) => OS_PRODUCT[computer.os]))]}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={data ? (computers.length === 0 ? "None yet" : computers.length) : undefined}
      unit={computers.length === 0 ? undefined : computers.length === 1 ? "computer" : "computers"}
      hint={
        !data
          ? undefined
          : open > 0
            ? `${open} open now`
            : data.engine.state === "running"
              ? "Ready to connect"
              : "Engine not running"
      }
    />
  )
}

export function SettingsTile() {
  const { report, loading } = useSelfUpdate()
  const behind = report?.releases.length ?? 0
  return (
    <ServiceTile
      icon={SettingsGear}
      title="Settings"
      href="/dashboard"
      loading={loading && !report}
      value={report ? (behind === 0 ? "Up to date" : behind) : undefined}
      unit={behind > 0 ? (behind === 1 ? "release behind" : "releases behind") : undefined}
      hint={report ? `v${report.version}${report.breaking ? " · breaking change" : ""}` : undefined}
    />
  )
}
