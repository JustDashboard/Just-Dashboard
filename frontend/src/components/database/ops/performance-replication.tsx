"use client"

import { bytes, duration, relativeTime, timestamp } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { Detail, DetailList, Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { readReplication } from "@/components/database/ops/performance-api"
import {
  Named,
  NoFigure,
  NotAvailable,
  Notes,
  Stale,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import type {
  DbReplicaSource,
  DbReplication,
  DbReplicationSlot,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

const ROLE: Record<DbReplication["role"], string> = {
  primary: "Primary",
  replica: "Replica",
  standalone: "Standalone",
  "": "Not reported",
}

/** How far behind something is. `-1` is the server's "unknown", which is not zero. */
function Lag({ seconds, size }: { seconds?: number; size?: number }) {
  const parts = [
    seconds !== undefined && seconds >= 0 ? duration(seconds) : undefined,
    size !== undefined && size >= 0 ? bytes(size) : undefined,
  ].filter(Boolean)
  if (parts.length === 0) return <span className="text-muted-foreground">not known here</span>
  const behind = (seconds ?? 0) > 0 || (size ?? 0) > 0
  return <span className="numeric">{behind ? parts.join(" · ") : "caught up"}</span>
}

/**
 * How this server's data reaches other servers, and how other servers' data
 * reaches it.
 *
 * It opens on what the server is — a primary others follow, a replica that
 * follows one, or standing alone — and then lists whichever sides it has:
 * the replicas connected to it with how far behind each is, the slots that
 * hold its log back for a consumer, what it publishes and subscribes to, and,
 * for a replica, the server it follows and whether both of its threads run.
 *
 * A slot nothing is reading keeps the log from being recycled until the disk
 * fills, so an inactive one is the reading to act on here. A server that
 * replicates nothing says so in a sentence, with the settings that would let
 * it; it is not an error.
 */
export function ReplicationView() {
  const { id, engine } = useDatabase()
  const replication = usePoll((signal) => readReplication(id, signal), 15_000, [id])
  return (
    <Panel plain aria-label="Replication">
      <PanelHeader title="Replication" actions={<Stale poll={replication} />} />
      <ViewRead
        poll={replication}
        what="the replication state"
        locking={engine.can("locks")}
        skeleton={<LoadingPanel plain rows={5} />}
      >
        {(data) => {
          if (!data.supported) {
            return (
              <div className="pt-4">
                <NotAvailable
                  title={`${engine.label}'s replication is not read from here`}
                  reason={data.reason}
                />
              </div>
            )
          }
          const idle = data.slots.filter((slot) => !slot.active).length
          const quiet =
            data.replicas.length === 0 &&
            data.slots.length === 0 &&
            data.publications.length === 0 &&
            data.subscriptions.length === 0 &&
            data.sources.length === 0
          return (
            <PanelBody className="animate-rise space-y-6">
              <MetricStrip>
                <Metric label="This server is" value={ROLE[data.role] ?? data.role} />
                <Metric label="Replicas connected" value={data.replicas.length.toLocaleString()} />
                {data.slots.length > 0 && (
                  <Metric
                    label="Slots"
                    value={data.slots.length.toLocaleString()}
                    hint={idle > 0 ? `${idle} with no reader` : "all being read"}
                  />
                )}
                {data.sources.length > 0 && (
                  <Metric label="Follows" value={data.sources[0].host ?? "another server"} />
                )}
              </MetricStrip>

              {quiet && (
                <EmptyNote>
                  This server is not replicating: nothing follows it, and it follows nothing.
                </EmptyNote>
              )}

              {data.sources.length > 0 && <Sources sources={data.sources} />}

              {data.replicas.length > 0 && (
                <Block title="Replicas">
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Replica</TableHead>
                        <TableHead className="px-2">State</TableHead>
                        <TableHead className="px-2">Behind</TableHead>
                        <TableHead className="px-2 @max-[46rem]:hidden">Replayed to</TableHead>
                        <TableHead className="@max-[38rem]:hidden">Connected</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.replicas.map((replica, index) => (
                        <TableRow key={`${replica.name}:${replica.client}:${index}`}>
                          <TableCell className="py-2">
                            <span className="flex items-center gap-2">
                              {replica.name ? <Named name={replica.name} mono /> : <NoFigure />}
                              {replica.client && (
                                <span className="font-mono text-hint text-muted-foreground">
                                  {replica.client}
                                </span>
                              )}
                            </span>
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <span className="flex items-center gap-2">
                              <Status
                                tone={replica.state === "streaming" ? "running" : "warning"}
                                label={replica.state || "unknown"}
                              />
                              {replica.syncState && <Tag>{replica.syncState}</Tag>}
                            </span>
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <Lag seconds={replica.lagSeconds} size={replica.lagBytes} />
                          </TableCell>
                          <TableCell className="px-2 py-2 font-mono text-muted-foreground @max-[46rem]:hidden">
                            {replica.replayLsn || <NoFigure />}
                          </TableCell>
                          <TableCell
                            className="py-2 text-muted-foreground @max-[38rem]:hidden"
                            title={timestamp(replica.since)}
                          >
                            {replica.since ? relativeTime(replica.since) : <NoFigure />}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </Block>
              )}

              {data.slots.length > 0 && <Slots slots={data.slots} />}

              {data.publications.length > 0 && (
                <Block title="Publications">
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Publication</TableHead>
                        <TableHead className="px-2">Tables</TableHead>
                        <TableHead className="px-2">Publishes</TableHead>
                        <TableHead className="@max-[38rem]:hidden">Owner</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.publications.map((publication) => (
                        <TableRow key={publication.name}>
                          <TableCell className="py-2 font-mono">{publication.name}</TableCell>
                          <TableCell className="numeric px-2 py-2">
                            {publication.allTables
                              ? "every table"
                              : publication.tables.toLocaleString()}
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <span className="flex flex-wrap gap-x-2.5">
                              {(["insert", "update", "delete", "truncate"] as const)
                                .filter((kind) => publication[kind])
                                .map((kind) => (
                                  <Tag key={kind}>{kind}</Tag>
                                ))}
                            </span>
                          </TableCell>
                          <TableCell className="py-2 @max-[38rem]:hidden">
                            {publication.owner ? <Named name={publication.owner} /> : <NoFigure />}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </Block>
              )}

              {data.subscriptions.length > 0 && (
                <Block title="Subscriptions">
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Subscription</TableHead>
                        <TableHead className="px-2">State</TableHead>
                        <TableHead className="px-2">To</TableHead>
                        <TableHead className="@max-[38rem]:hidden">Last message</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.subscriptions.map((subscription) => (
                        <TableRow key={subscription.name}>
                          <TableCell className="py-2 font-mono">{subscription.name}</TableCell>
                          <TableCell className="px-2 py-2">
                            <Status
                              tone={
                                !subscription.enabled
                                  ? "stopped"
                                  : subscription.running
                                    ? "running"
                                    : "warning"
                              }
                              label={
                                !subscription.enabled
                                  ? "Disabled"
                                  : subscription.running
                                    ? "Receiving"
                                    : "Not running"
                              }
                            />
                          </TableCell>
                          <TableCell className="px-2 py-2 font-mono">
                            {subscription.publications.join(", ")}
                          </TableCell>
                          <TableCell
                            className="py-2 text-muted-foreground @max-[38rem]:hidden"
                            title={timestamp(subscription.lastMessage)}
                          >
                            {subscription.lastMessage ? (
                              relativeTime(subscription.lastMessage)
                            ) : (
                              <NoFigure />
                            )}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </Block>
              )}

              {data.facts && Object.keys(data.facts).length > 0 && (
                <section className="space-y-2">
                  <p className="eyebrow">Settings that decide it</p>
                  <DetailList>
                    {Object.entries(data.facts)
                      .sort(([a], [b]) => a.localeCompare(b))
                      .map(([name, value]) => (
                        <Detail key={name} label={<span className="font-mono">{name}</span>}>
                          <span className="font-mono wrap-anywhere">{value || "(empty)"}</span>
                        </Detail>
                      ))}
                  </DetailList>
                </section>
              )}

              <Notes notes={data.notes} />
            </PanelBody>
          )
        }}
      </ViewRead>
    </Panel>
  )
}

/** A named table inside the view, bled to the panel's edge like the tables of the views beside it. */
function Block({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-1">
      <p className="eyebrow">{title}</p>
      {/* A container: the tables inside give up a column by this block's
          width, not the window's. */}
      <div className="@container -mx-4">{children}</div>
    </section>
  )
}

function Slots({ slots }: { slots: DbReplicationSlot[] }) {
  return (
    <Block title="Slots">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Slot</TableHead>
            <TableHead className="px-2">Read by</TableHead>
            <TableHead className="px-2 text-right">Log held back</TableHead>
            <TableHead className="@max-[38rem]:hidden">Kind</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {slots.map((slot) => (
            <TableRow key={slot.name}>
              <TableCell className="py-2 font-mono">{slot.name}</TableCell>
              <TableCell className="px-2 py-2">
                {/* An idle slot keeps the log from being recycled: it is the
                    reading this table exists for. */}
                <Status
                  tone={slot.active ? "running" : "warning"}
                  label={slot.active ? "A reader" : "Nothing — it holds the log"}
                />
              </TableCell>
              <TableCell className="numeric px-2 py-2 text-right">
                {slot.retainedBytes < 0 ? <NoFigure /> : bytes(slot.retainedBytes)}
              </TableCell>
              <TableCell className="py-2 @max-[38rem]:hidden">
                <span className="flex flex-wrap items-center gap-x-2.5">
                  {slot.type && <Tag>{slot.type}</Tag>}
                  {slot.plugin && <Tag mono>{slot.plugin}</Tag>}
                  {slot.temporary && <Tag>temporary</Tag>}
                  {slot.walStatus && slot.walStatus !== "reserved" && (
                    <Tag tone={slot.walStatus === "lost" ? "danger" : "warning"}>
                      {slot.walStatus}
                    </Tag>
                  )}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Block>
  )
}

/** The server this one follows: where it is, whether both halves of the link run, how far behind. */
function Sources({ sources }: { sources: DbReplicaSource[] }) {
  return (
    <section className="space-y-3">
      <p className="eyebrow">Follows</p>
      {sources.map((source, index) => {
        const up = source.ioRunning && source.sqlRunning
        return (
          <div key={`${source.channel}:${index}`} className="space-y-2">
            <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-body">
              <span className="font-mono font-medium">
                {source.host ?? "unknown"}
                {source.port ? `:${source.port}` : ""}
              </span>
              <Status
                tone={up ? "running" : "danger"}
                label={
                  up
                    ? "Receiving and applying"
                    : !source.ioRunning && !source.sqlRunning
                      ? "Stopped"
                      : source.ioRunning
                        ? "Receiving, not applying"
                        : "Applying, not receiving"
                }
              />
              {source.channel && <Tag mono>{source.channel}</Tag>}
            </p>
            <DetailList>
              <Detail label="Behind">
                <Lag seconds={source.lagSeconds} />
              </Detail>
              {source.user && <Detail label="Signs in as">{source.user}</Detail>}
              {source.state && <Detail label="State">{source.state}</Detail>}
              {source.position && (
                <Detail label="Position" className="font-mono wrap-anywhere">
                  {source.position}
                </Detail>
              )}
              {source.gtid && (
                <Detail label="GTID" className="font-mono wrap-anywhere">
                  {source.gtid}
                </Detail>
              )}
              {source.lastError && (
                <Detail label="Last error" className="wrap-anywhere text-destructive">
                  {source.lastError}
                </Detail>
              )}
            </DetailList>
          </div>
        )
      })}
    </section>
  )
}
