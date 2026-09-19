"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { useRouter } from "next/navigation"
import { ListOrdered, Router, Shield } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { Listener } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { DANGEROUS_PORTS } from "@/components/proxy/attention"
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
  const { data, error, loading } = usePoll(
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
      dangerous: all.filter((l) => l.exposed && DANGEROUS_PORTS[l.port]).length,
    }),
    [all],
  )
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return all.filter((l) => {
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
  }, [all, filter, reach])

  const verbsFor = (l: Listener): Verb[] => {
    const verbs: Verb[] = []
    if (l.pid > 0) {
      verbs.push({
        key: "process",
        label: "Process",
        detail: "Open it under Processes: connections, open files, parent chain, and its verbs.",
        icon: ListOrdered,
        inline: true,
        run: () => router.push(`/processes?pid=${l.pid}`),
      })
    }
    if (l.exposed) {
      verbs.push({
        key: "firewall",
        label: "Firewall",
        detail:
          "Whether the firewall lets the internet reach this port, and the rule that decides.",
        icon: Shield,
        run: () => router.push("/security/firewall"),
      })
    }
    return verbs
  }

  const header = <PageHeader eyebrow="Proxy" title="Listening ports" />

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
        <ErrorState error={error} />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4}>
        <StatTile
          label="Listening"
          value={counts.all}
          hint={`${counts.tcp} TCP · ${counts.udp} UDP`}
        />
        <StatTile
          label="Exposed"
          value={counts.exposed}
          tone={counts.exposed > 0 ? "warning" : "success"}
          hint={counts.exposed > 0 ? "bound to every interface" : "nothing off the machine"}
        />
        <StatTile
          label="Loopback"
          value={counts.loopback}
          hint="reachable from this machine only"
        />
        <StatTile
          label="Databases exposed"
          value={counts.dangerous}
          tone={counts.dangerous > 0 ? "danger" : "default"}
          hint={
            counts.dangerous > 0
              ? "a database or control port on every interface"
              : "none on a public address"
          }
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader title="Sockets" />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Port, process, user or address"
            containerClassName="sm:w-64"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {(Object.keys(REACH_LABEL) as Reach[]).map((key) => (
              <FilterChip key={key} selected={reach === key} onClick={() => setReach(key)}>
                {REACH_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ))}
          </div>
        </PanelToolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            <EmptyState icon={Router} title="No sockets match" className="mt-4" />
          ) : (
            <>
              <div className="-mx-4 hidden min-w-0 md:block">
                <Table containerClassName="max-h-[calc(100svh-24rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-20">Port</TableHead>
                      <TableHead className="w-16">Proto</TableHead>
                      <TableHead>Bound to</TableHead>
                      <TableHead className="w-full">Process</TableHead>
                      <TableHead className="hidden lg:table-cell">User</TableHead>
                      <TableHead>Reach</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((listener, i) => (
                      <TableRow
                        key={`${listener.protocol}-${listener.address}-${listener.port}-${listener.pid}-${i}`}
                        className="group"
                      >
                        <TableCell className="numeric font-mono text-body">
                          {listener.port}
                        </TableCell>
                        <TableCell className="text-muted-foreground uppercase">
                          {listener.protocol}
                        </TableCell>
                        <TableCell className="font-mono">{listener.address || "*"}</TableCell>
                        <TableCell>
                          <div className="max-w-[26rem] min-w-0">
                            <div className="truncate text-body">
                              {listener.process || "unknown"}
                            </div>
                            <p className="truncate font-mono text-hint text-muted-foreground">
                              {listener.cmdline}
                            </p>
                          </div>
                        </TableCell>
                        <TableCell className="hidden lg:table-cell">
                          {listener.user ?? "—"}
                        </TableCell>
                        <TableCell>
                          <ReachStatus listener={listener} />
                        </TableCell>
                        <TableCell>
                          <VerbActions dim verbs={verbsFor(listener)} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <ul className="divide-y divide-hairline md:hidden">
                {visible.map((listener, i) => (
                  <li
                    key={`${listener.protocol}-${listener.address}-${listener.port}-${listener.pid}-${i}`}
                    className={cn(
                      "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                      ROW_BLEED,
                    )}
                  >
                    <span className="numeric w-12 shrink-0 font-mono text-body">
                      {listener.port}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-body">{listener.process || "unknown"}</div>
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
                    <VerbActions verbs={verbsFor(listener)} className="shrink-0" />
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

function ReachStatus({ listener }: { listener: Listener }) {
  if (!listener.exposed) return <span className="text-xs text-muted-foreground">loopback</span>
  const service = DANGEROUS_PORTS[listener.port]
  return (
    <Status
      verdict={service ? "critical" : "warning"}
      label={service ? `${service} exposed` : "exposed"}
      icon={Router}
    />
  )
}
