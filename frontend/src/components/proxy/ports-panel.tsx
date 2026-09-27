"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { useRouter } from "next/navigation"
import { ListOrdered, Router, Shield } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { Listener } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, portProduct, processProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { DANGEROUS_PORTS } from "@/components/proxy/attention"
import { dangerousPorts, exposedHint, reachVerdict } from "@/components/proxy/ports"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Reach = "all" | "exposed" | "loopback" | "tcp" | "udp"

const REACH_LABEL: Record<Reach, string> = {
  all: "All",
  exposed: "Exposed",
  loopback: "Loopback",
  tcp: "TCP",
  udp: "UDP",
}

/**
 * Every listening socket on the host, and whether it faces off the machine.
 *
 * "What is listening on 8080 and who started it" is the first question during
 * an incident, and the answer to the second half is one click away: a row's
 * process opens under Processes with its connections and parent chain.
 */
export function PortsPage() {
  const router = useRouter()
  const [filter, setFilter] = useSessionState("proxy.ports.query", "")
  const [reach, setReach] = useSessionState<Reach>("proxy.ports.reach", "all")
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<Listener[]>("/ports", undefined, signal),
    15_000,
  )

  const all = useMemo(() => data ?? [], [data])
  const counts = useMemo(
    () => ({
      all: all.length,
      exposed: all.filter((l) => l.exposed).length,
      loopback: all.filter((l) => !l.exposed).length,
      tcp: all.filter((l) => l.protocol === "tcp").length,
      udp: all.filter((l) => l.protocol === "udp").length,
    }),
    [all],
  )
  const dangerous = useMemo(() => dangerousPorts(all), [all])
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return all
      .filter((l) => {
        if (reach === "exposed" && !l.exposed) return false
        if (reach === "loopback" && l.exposed) return false
        if (reach === "tcp" && l.protocol !== "tcp") return false
        if (reach === "udp" && l.protocol !== "udp") return false
        if (!needle) return true
        return (
          String(l.port).includes(needle) ||
          (l.process ?? "").toLowerCase().includes(needle) ||
          (l.cmdline ?? "").toLowerCase().includes(needle) ||
          (l.user ?? "").toLowerCase().includes(needle) ||
          l.address.includes(needle)
        )
      })
      .sort((a, b) => {
        const rank = (listener: Listener) =>
          listener.exposed ? (DANGEROUS_PORTS[listener.port] ? 0 : 1) : 2
        return rank(a) - rank(b) || a.port - b.port
      })
  }, [all, filter, reach])

  const verbsFor = (l: Listener): Verb[] => {
    const verbs: Verb[] = []
    if (l.pid > 0) {
      verbs.push({
        key: "process",
        label: "Process",
        icon: ListOrdered,
        inline: true,
        run: () => router.push(`/processes?pid=${l.pid}`),
      })
    }
    if (l.exposed) {
      verbs.push({
        key: "firewall",
        label: "Firewall",
        icon: Shield,
        run: () => router.push("/security/firewall"),
      })
    }
    return verbs
  }

  const header = <PageContext eyebrow="Proxy" title="Listening ports" />

  if (loading && !data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (error && !data) {
    return (
      <Page>
        {header}
        <ErrorState error={error} onRetry={refresh} />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4} dense>
        <StatTile
          label="Listening"
          value={counts.all}
          hint={`${counts.tcp} TCP · ${counts.udp} UDP`}
        />
        <StatTile
          label="Exposed"
          value={counts.exposed}
          tone={counts.exposed > 0 ? "warning" : "success"}
          hint={exposedHint(all)}
        />
        <StatTile
          label="Loopback"
          value={counts.loopback}
          hint="reachable from this machine only"
        />
        <StatTile
          label="Databases exposed"
          value={dangerous.length}
          tone={
            dangerous.some((d) => d.internet)
              ? "danger"
              : dangerous.length > 0
                ? "warning"
                : "default"
          }
          hint={dangerous.length > 0 ? "counted once per port" : "none off the machine"}
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader
          title="Listening sockets"
          actions={<span className="text-hint text-muted-foreground">Refreshes every 15s</span>}
        />
        <Toolbar className="justify-between gap-x-4">
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Port, process, user or address"
          />
          <ChipStrip>
            {(Object.keys(REACH_LABEL) as Reach[]).map((key) => (
              <FilterChip key={key} selected={reach === key} onClick={() => setReach(key)}>
                {REACH_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        </Toolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            <EmptyState icon={Router} title="No sockets match" className="mt-4" />
          ) : (
            <>
              <div className="hidden min-w-0 lg:block">
                {/* The grid scrolls independently, so its border marks that boundary. */}
                <Table
                  className="table-fixed"
                  containerClassName="max-h-[calc(100svh-24rem)] rounded-xl border bg-card"
                >
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-[20%]">Endpoint</TableHead>
                      <TableHead>Application</TableHead>
                      <TableHead className="w-44">Reach</TableHead>
                      <TableHead className="w-36">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((listener, i) => (
                      <TableRow
                        key={`${listener.protocol}-${listener.address}-${listener.port}-${listener.pid}-${i}`}
                        className="group"
                      >
                        <TableCell className="py-4">
                          <div className="flex items-baseline gap-2">
                            <span className="numeric font-mono text-title font-semibold">
                              {listener.port}
                            </span>
                            <span className="text-hint text-muted-foreground uppercase">
                              {listener.protocol}
                            </span>
                          </div>
                          <p
                            className="mt-1 truncate font-mono text-hint text-muted-foreground"
                            title={listener.address}
                          >
                            {listener.address || "*"}
                          </p>
                        </TableCell>
                        <TableCell>
                          <div className="flex min-w-0 items-center gap-3">
                            <ProcessMark listener={listener} />
                            <div className="min-w-0">
                              <p className="truncate text-body font-medium">
                                {listener.process || "unknown"}
                              </p>
                              <p
                                className="truncate font-mono text-hint text-muted-foreground"
                                title={listener.cmdline}
                              >
                                {listener.cmdline || "No command reported"}
                              </p>
                              <p className="mt-1 text-hint text-muted-foreground">
                                {listener.user || "unknown user"}
                                {listener.pid > 0 && ` · PID ${listener.pid}`}
                              </p>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <ReachStatus listener={listener} />
                        </TableCell>
                        <TableCell>
                          <VerbBar
                            verbs={verbsFor(listener)}
                            menuLabel={`Actions for ${listener.protocol} port ${listener.port}`}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <ul className="divide-y divide-hairline lg:hidden">
                {visible.map((listener, i) => (
                  <li
                    key={`${listener.protocol}-${listener.address}-${listener.port}-${listener.pid}-${i}`}
                    className={cn(
                      "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                      ROW_BLEED,
                    )}
                  >
                    <span className="numeric w-14 shrink-0 font-mono text-title font-semibold">
                      {listener.port}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 items-center gap-2 text-body">
                        <ProcessMark listener={listener} />
                        <span className="truncate">{listener.process || "unknown"}</span>
                      </div>
                      {/* Which address a port is bound to is the whole reason
                          this page exists — it must not be the line that gets
                          dropped on a narrow screen. */}
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {listener.address || "*"}
                        <span className="uppercase"> · {listener.protocol}</span>
                        {listener.user && ` · ${listener.user}`}
                      </p>
                      <div className="mt-1.5">
                        <ReachStatus listener={listener} />
                      </div>
                    </div>
                    <VerbBar verbs={verbsFor(listener)} className="shrink-0" />
                  </li>
                ))}
              </ul>
            </>
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}

/**
 * The process as the product it is, bare at the line's height — Postgres on
 * 5432, nginx on 443 — the way the identity line draws a host's facts. Read
 * from the process name first and the port second, so a `postgres` on an
 * unusual port is still Postgres and a `python` on 5432 is not.
 */
function ProcessMark({ listener }: { listener: Listener }) {
  const product = processProduct(listener.process ?? "") ?? portProduct(listener.port)
  return <ProductLogo id={product} size="sm" fallback={Router} />
}

/** Coloured by who can connect, as the posture levels the same socket. */
function ReachStatus({ listener }: { listener: Listener }) {
  const verdict = reachVerdict(listener)
  if (!verdict) return <span className="text-xs text-muted-foreground">loopback</span>
  const service = DANGEROUS_PORTS[listener.port]
  return (
    <Status verdict={verdict} label={service ? `${service} exposed` : "exposed"} icon={Router} />
  )
}
