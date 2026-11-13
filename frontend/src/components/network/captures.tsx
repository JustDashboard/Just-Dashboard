"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import {
  Crosshair,
  Download,
  FileText,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  StopCircle,
  Trash,
  Wrench,
  type Icon,
} from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, downloadUrl, get, post } from "@/lib/api"
import { bytes, plural, timestamp } from "@/lib/format"
import {
  captureFinished,
  captureReading,
  type CaptureDraft,
  type CaptureList,
  type CaptureRun,
} from "@/lib/network-captures"
import { captureLine, captureMatches, captureTotals } from "@/lib/network-runs-view"
import { useSessionState } from "@/lib/view-state"
import { CaptureCreate } from "./capture-create"
import { StateTile } from "./diagnostic-marks"
import { NetworkReadWarning } from "./read-warning"

/** The list's search, shared with the rail on a capture's page. */
export const CAPTURE_QUERY_KEY = "network.captures.query"

export const captureHref = (id: string, tab?: string) =>
  `/network/captures/${encodeURIComponent(id)}${tab && tab !== "overview" ? `?tab=${tab}` : ""}`

/**
 * Reading register: the captures this server keeps, and the one way to start
 * another.
 *
 * Four grey tiles opened it — capturing, retained, packet data, retention —
 * over the list beside a pane of seventeen facts. The figures are the
 * identity line's now (the packet data in bytes: rounded to KiB it read
 * "0 KiB" for a 412-byte capture), and a capture is a page of its own
 * (`capture-page.tsx`), so the list is one card per capture.
 */
export function Captures() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  const seed = captureSeed(params)
  // A hand-off from the quick snapshot opens setup with its settings; nothing
  // is captured until the operator starts it.
  const [open, setOpen] = useState(() => Boolean(seed))
  const [query, setQuery] = useSessionState(CAPTURE_QUERY_KEY, "")
  // The address that selected a capture beside the list opens its page.
  const legacy = params.get("capture")
  useEffect(() => {
    if (legacy) router.replace(captureHref(legacy))
  }, [legacy, router])
  const poll = usePoll(
    (signal) => get<CaptureList>("/network/captures/", undefined, signal),
    2500,
    [admin],
    { enabled: admin && !legacy },
  )

  if (!admin)
    return (
      <>
        <PageContext title="Packet captures" />
        <EmptyState
          icon={Crosshair}
          title="Packet captures require the admin capability"
          description="Original packet bytes and private filter tuples are available only to server administrators."
        />
      </>
    )

  const list = poll.data
  const runs = list?.runs ?? []
  const shown = runs.filter((run) => captureMatches(run, query))
  return (
    <>
      <PageContext title="Packet captures" />
      <CapturesIdentity
        list={list}
        onNew={() => setOpen(true)}
        onRefresh={poll.refresh}
        onSummary={() => router.push("/network/tools?tool=capture")}
      />
      {list && (
        <NetworkReadWarning
          error={poll.error}
          refresh={poll.refresh}
          lastSuccess={poll.lastSuccess}
          reading="packet captures"
        />
      )}
      {!list && poll.error ? (
        <ErrorState error={poll.error} onRetry={poll.refresh} />
      ) : list && runs.length === 0 ? (
        <EmptyState
          icon={Crosshair}
          title="No packet captures"
          description="A capture keeps what one interface sees, bounded and private, for 24 hours."
          action={
            <span className="flex flex-wrap justify-center gap-2">
              <Button size="sm" onClick={() => setOpen(true)}>
                <Plus className="size-3.5" />
                New capture
              </Button>
              <Button asChild size="sm" variant="outline">
                <Link href="/network/tools?tool=capture">Quick packet summary</Link>
              </Button>
            </span>
          }
        />
      ) : (
        <Panel plain aria-label="Captures">
          <PanelHeader
            title="Captures"
            actions={
              <span className="flex min-w-0 flex-wrap items-center gap-3">
                <SearchInput
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Name, interface or address"
                  aria-label="Search the captures"
                />
                <span className="numeric text-hint text-muted-foreground">
                  {list ? plural(runs.length, "capture") : " "}
                </span>
              </span>
            }
          />
          <PanelBody>
            {!list ? (
              <LoadingRows rows={3} className="[&>*]:h-14 [&>*]:rounded-xl" />
            ) : shown.length === 0 ? (
              <EmptyNote>
                No capture matches.{" "}
                <button
                  type="button"
                  className="rounded-sm font-medium text-foreground focus-ring hover:underline"
                  onClick={() => setQuery("")}
                >
                  Clear the search
                </button>
              </EmptyNote>
            ) : (
              <ChoiceList aria-label="Packet captures">
                {shown.map((run) => (
                  <CaptureRow key={run.id} run={run} onChanged={poll.refresh} />
                ))}
              </ChoiceList>
            )}
          </PanelBody>
        </Panel>
      )}
      <CaptureCreate
        seed={seed}
        open={open}
        onOpenChange={setOpen}
        onCreated={(id) => router.push(captureHref(id))}
      />
    </>
  )
}

/**
 * The page's opening line: what is kept and what is running, the retention
 * the server holds them to, and New capture as the one command.
 */
function CapturesIdentity({
  list,
  onNew,
  onRefresh,
  onSummary,
}: {
  list?: CaptureList
  onNew: () => void
  onRefresh: () => void
  onSummary: () => void
}) {
  const totals = list ? captureTotals(list) : undefined
  return (
    <HostIdentity
      label="About packet captures"
      fallback={Crosshair}
      title="Packet captures"
      state={
        totals ? (
          totals.capturing > 0 ? (
            <Status tone="running" label={`${totals.capturing} capturing`} />
          ) : (
            <Status tone="stopped" label="Nothing capturing" />
          )
        ) : (
          <TextShimmer className="text-xs">Reading…</TextShimmer>
        )
      }
      facts={
        list && totals ? (
          <>
            <span className="numeric">
              {totals.retained} of {list.maxRetained} kept
            </span>
            <FactDot />
            <span className="numeric">{bytes(totals.bytes)} of packet data</span>
            <FactDot />
            <span className="numeric">kept {list.retentionHours} hours</span>
            <FactDot />
            <span className="numeric">up to {list.maxRunning} at once</span>
          </>
        ) : (
          <span>&nbsp;</span>
        )
      }
      command={
        <Button size="sm" onClick={onNew}>
          <Plus className="size-3.5" />
          New capture
        </Button>
      }
      menu={
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label="Capture actions">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            <DropdownMenuItem onSelect={onRefresh}>
              <RefreshClockwise className="size-3.5" />
              Refresh
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onSummary}>
              <Wrench className="size-3.5" />
              Quick packet summary
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      }
    />
  )
}

function CaptureRow({ run, onChanged }: { run: CaptureRun; onChanged: () => void }) {
  const reading = captureReading(run)
  const actions = useCaptureActions(run, { onChanged, onDeleted: onChanged })
  return (
    <ChoiceRow
      workspaceItem={{ id: run.id, name: run.name }}
      leading={<StateTile glyph={Crosshair} tone={reading.tone} />}
      title={run.name}
      verb={`Open ${run.name}`}
      href={captureHref(run.id)}
      busy={!captureFinished(run)}
      description={<span className="font-mono">{captureLine(run)}</span>}
      trailing={
        <span className="flex items-center gap-4">
          <span className="numeric w-32 truncate text-right text-hint text-muted-foreground max-md:hidden">
            {run.result
              ? `${plural(run.result.packets, "packet")} · ${bytes(run.result.bytes)}`
              : "—"}
          </span>
          <time
            dateTime={run.createdAt}
            className="numeric w-36 truncate text-right text-hint text-muted-foreground max-lg:hidden"
          >
            {timestamp(run.createdAt)}
          </time>
          <span className="flex w-36 justify-end max-sm:hidden">
            <Status {...reading} />
          </span>
        </span>
      }
      actions={
        <>
          <CaptureMenu run={run} actions={actions} />
          {actions.dialog}
        </>
      }
    />
  )
}

type CaptureActions = ReturnType<typeof useCaptureActions>

/**
 * What can be done to a capture, decided once for its row and its page: stop
 * it while it runs, download the original bytes once it has kept them, take
 * the redacted support export at any time, delete it once it has ended — the
 * last only for an account the server lets delete.
 */
export function useCaptureActions(
  run: CaptureRun,
  { onChanged, onDeleted }: { onChanged: () => void; onDeleted: () => void },
) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [stopping, setStopping] = useState(false)
  const [error, setError] = useState<Error>()
  const finished = captureFinished(run)
  const id = encodeURIComponent(run.id)
  const stop = async () => {
    if (stopping) return
    setStopping(true)
    setError(undefined)
    try {
      await post(`/network/captures/${id}/cancel`, {})
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setStopping(false)
    }
  }
  return {
    dialog,
    error,
    stopping: stopping || run.status === "cancelling",
    pcap:
      finished && run.result?.artifactAvailable
        ? downloadUrl(`/network/captures/${run.id}/pcap`)
        : undefined,
    support: downloadUrl(`/network/captures/${run.id}/support`),
    stop: finished ? undefined : () => void stop(),
    remove:
      finished && can("destructive")
        ? () =>
            confirm({
              title: "Delete packet capture",
              subject: {
                mark: <StateTile glyph={Crosshair} tone={captureReading(run).tone} size="sm" />,
                name: run.name,
                facts: <span className="font-mono">{captureLine(run)}</span>,
              },
              description: `Delete the retained packet bytes and metadata for ${run.name}.`,
              confirmLabel: "Delete capture",
              action: async () => {
                await del(`/network/captures/${id}`)
              },
              onDone: onDeleted,
            })
        : undefined,
  }
}

/** The capture's verbs as words, downloads as links so the browser saves them. */
export function CaptureMenu({
  run,
  actions,
  omit = [],
  label,
}: {
  run: CaptureRun
  actions: CaptureActions
  /** Verbs the surface already draws as its command. */
  omit?: ("pcap" | "stop")[]
  label?: string
}) {
  const item = (icon: Icon, words: string) => {
    const Glyph = icon
    return (
      <>
        <Glyph className="size-3.5" />
        {words}
      </>
    )
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={label ?? `Actions for ${run.name}`}
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        {actions.pcap && !omit.includes("pcap") && (
          <DropdownMenuItem asChild>
            <a href={actions.pcap} download>
              {item(Download, "Download original PCAP")}
            </a>
          </DropdownMenuItem>
        )}
        <DropdownMenuItem asChild>
          <a href={actions.support} download>
            {item(FileText, "Export redacted support")}
          </a>
        </DropdownMenuItem>
        {actions.stop && !omit.includes("stop") && (
          <DropdownMenuItem disabled={actions.stopping} onSelect={actions.stop}>
            {item(StopCircle, actions.stopping ? "Stopping…" : "Stop capture")}
          </DropdownMenuItem>
        )}
        {actions.remove && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={actions.remove}>
              {item(Trash, "Delete")}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const SNAPSHOT_PROTOCOLS = ["all", "tcp", "udp", "icmp", "icmp6"] as const

/** The interface and protocol a quick packet snapshot links here with. */
function captureSeed(params: {
  get(key: string): string | null
}): Partial<CaptureDraft> | undefined {
  const iface = params.get("interface")?.trim()
  if (!iface || !/^[A-Za-z0-9._@:-]{1,15}$/.test(iface)) return undefined
  const asked = params.get("protocol") ?? "all"
  const protocol = (SNAPSHOT_PROTOCOLS as readonly string[]).includes(asked)
    ? (asked as CaptureDraft["protocol"])
    : "all"
  return {
    interface: iface,
    protocol,
    family: protocol === "icmp6" ? "inet6" : "inet",
    name: `Snapshot follow-up · ${iface}`,
  }
}
