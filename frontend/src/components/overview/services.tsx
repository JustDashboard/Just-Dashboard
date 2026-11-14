"use client"

import { useMemo } from "react"
import {
  Box,
  GitHubMark,
  Globe,
  Puzzle,
  Servers,
  Shield,
  TerminalWindow,
  type Icon,
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
import { Bone } from "@/components/bone"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { LogoGlyph } from "@/components/logo"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import {
  ProductGlyphs,
  ProductLogo,
  containerProducts,
  hostProduct,
  platformProduct,
} from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { cn } from "@/lib/utils"
import {
  certificateWords,
  dockerWords,
  gitWords,
  packageWords,
  remoteWords,
  unitWords,
  versionWords,
  type ServiceWords,
} from "./service-words"
import { unitCounts } from "./order"

/**
 * A module that is genuinely absent on this host, as opposed to a poll that
 * failed once. The dashboard returns a precise code for the former; a dropped
 * VPN tunnel — which is a normal Tuesday here — is anything else, and must not
 * turn a row that read "12 running" into a false claim about the host.
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
 * The modules as a run of rows you open, two to a line from `lg`: the shape
 * Databases and Backups above them have. They were eight tiles, a figure over
 * a unit over a caption each ("9 / running / 11 containers · 2 stopped"),
 * which the reader had to turn into the sentence a row now says.
 */
export function ServiceList({ children }: { children: React.ReactNode }) {
  return (
    <ChoiceList aria-label="Services" className="grid gap-2 space-y-0 lg:grid-cols-2">
      {children}
    </ChoiceList>
  )
}

/**
 * One module: the product it is, one sentence, a state, and the way to its
 * page. A row is drawn from the first frame with its name on it and is one
 * height in every state: the sentence's place holds a bone while its read is
 * out, and says so in words where the module is not on this host or its read
 * failed. The sentence rises once when it lands (§11 *arrived*).
 *
 * The products the sentence counts stand before the state, as they stood
 * after a tile's hint: the images running, the forges pushed to.
 */
function ServiceRow({
  product,
  logo,
  fallback,
  title,
  href,
  words,
  loading,
  unavailable,
  products = [],
}: {
  /** The module drawn as itself: a `product-logo` id. */
  product?: string
  /** A mark that is no product's file, in the tile's place. */
  logo?: React.ReactNode
  fallback: Icon
  title: string
  href: string
  /** What the read says. Undefined while it is out, and when it failed. */
  words?: ServiceWords
  loading?: boolean
  unavailable?: boolean
  /** What the sentence counts, as the products themselves. */
  products?: string[]
}) {
  const state: { tone: DotTone; label: string } | undefined = loading
    ? undefined
    : unavailable
      ? { tone: "stopped", label: "Not installed" }
      : (words?.state ?? { tone: "warning", label: "Unreachable" })
  return (
    <ChoiceRow
      href={href}
      verb={`Open ${title}`}
      leading={logo ?? <ProductLogo id={product} size="sm" fallback={fallback} />}
      title={title}
      description={
        loading ? (
          <Bone className="w-40" />
        ) : (
          <span
            key="settled"
            className="inline-block max-w-full animate-rise truncate align-bottom"
          >
            {unavailable ? "Not on this server" : (words?.sentence ?? "Its reading did not arrive")}
          </span>
        )
      }
      trailing={
        state && (
          <>
            {/* Off on a phone, where the sentence needs the width. */}
            {words && (
              <span className="flex max-sm:hidden">
                <ProductGlyphs ids={products} max={4} />
              </span>
            )}
            <Status
              tone={state.tone}
              label={state.label}
              // On a phone only a state that asks for something keeps its
              // word: the sentence has said the rest and needs the width.
              className={cn(
                "animate-rise",
                state.tone !== "warning" && state.tone !== "danger" && "max-sm:hidden",
              )}
            />
          </>
        )
      }
    />
  )
}

export function DockerRow({ read }: { read: PollState<Container[]> }) {
  const { data, error, loading } = read
  const running = data?.filter((c) => c.state === "running") ?? []
  return (
    <ServiceRow
      product="docker"
      fallback={Box}
      title="Docker"
      href="/docker"
      products={containerProducts(running)}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      words={data && dockerWords(data.length, running.length)}
    />
  )
}

export function ProxyRow({ read }: { read: PollState<Certificate[]> }) {
  const { data, error, loading } = read
  const soonest = useMemo(() => {
    if (!data || data.length === 0) return undefined
    return [...data].sort((a, b) => a.daysLeft - b.daysLeft)[0]
  }, [data])
  return (
    <ServiceRow
      product="nginx-static"
      fallback={Globe}
      title="Proxy & TLS"
      href="/proxy"
      products={data && data.length > 0 ? ["lets-encrypt"] : []}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      words={data && certificateWords(data.length, soonest)}
    />
  )
}

const VERDICT_TONE = {
  ok: "running",
  notice: "notice",
  warning: "warning",
  critical: "danger",
} as const
const VERDICT_WORD = {
  ok: "Private",
  notice: "Private",
  warning: "Public",
  critical: "Open",
} as const

export function SecurityRow({ read }: { read: PollState<Exposure> }) {
  const { data, error, loading } = read
  const grade = data ? EXPOSURE_GRADE[data.grade] : undefined
  return (
    <ServiceRow
      product={data?.grade === "tailscale" ? "tailscale" : undefined}
      fallback={Shield}
      title="Security"
      href="/security"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      words={
        data && grade
          ? {
              sentence: `${grade.label}, ${plural(data.allowlist.length, "allowed range")} on ${plural(data.interfaces.length, "interface")}`,
              state: { tone: VERDICT_TONE[grade.verdict], label: VERDICT_WORD[grade.verdict] },
            }
          : undefined
      }
    />
  )
}

export function PackagesRow({
  read,
  platform,
}: {
  read: PollState<UpdateReport>
  platform: string
}) {
  const { data, error, loading } = read
  return (
    <ServiceRow
      product={platformProduct(platform)}
      fallback={Puzzle}
      title="Packages"
      href="/packages"
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      words={data && packageWords(data)}
    />
  )
}

/**
 * What systemd runs, and what it gave up on: a failed unit is the one thing on
 * the machine that stopped and will not try again by itself, so while there
 * is one it leads the sentence and the state is red.
 */
export function UnitsRow({ read }: { read: PollState<SystemdList> }) {
  const { data, error, loading } = read
  return (
    <ServiceRow
      product="systemd"
      fallback={Servers}
      title="System services"
      href="/processes/services"
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      words={data && unitWords(unitCounts(data.units))}
    />
  )
}

/**
 * The working copies on this server, with the forges they push to, and what
 * is waiting in them: uncommitted work first, because it is the one thing
 * here a deploy or a restore cannot bring back.
 */
export function GitRow({ read }: { read: PollState<{ available: boolean; repos: GitRepo[] }> }) {
  const { data, error, loading } = read
  const repos = data?.repos ?? []
  const forges = [...new Set(repos.flatMap((repo) => hostProduct(repo.remote) ?? []))]
  return (
    <ServiceRow
      product="git"
      fallback={GitHubMark}
      title="Git"
      href="/git"
      products={forges}
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      words={data && gitWords(repos)}
    />
  )
}

const OS_PRODUCT = { windows: "windows", macos: "apple", linux: "linux" } as const

/** The computers the screen can be opened on, each drawn as its system, and which are open now. */
export function RemoteDesktopRow({ read }: { read: PollState<RdOverview> }) {
  const { data, error, loading } = read
  const computers = data?.computers ?? []
  return (
    <ServiceRow
      product="guacamole"
      fallback={TerminalWindow}
      title="Remote Desktop"
      href="/remote-desktop"
      products={[...new Set(computers.map((computer) => OS_PRODUCT[computer.os]))]}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      words={
        data &&
        remoteWords(
          computers.length,
          computers.filter((computer) => computer.live > 0).length,
          data.engine.state === "running",
        )
      }
    />
  )
}

export function SettingsRow() {
  const { report, loading } = useSelfUpdate()
  return (
    <ServiceRow
      logo={
        <span
          aria-hidden
          className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-brand"
        >
          <LogoGlyph small className="h-4 w-auto" />
        </span>
      }
      fallback={Servers}
      title="Settings"
      href="/dashboard"
      loading={loading && !report}
      words={report && versionWords(report.version, report.releases.length, report.breaking)}
    />
  )
}
