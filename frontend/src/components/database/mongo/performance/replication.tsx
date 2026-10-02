"use client"

import { bytes, duration, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Meter } from "@/components/meter"
import { Metric, MetricStrip } from "@/components/page"
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
import { mongoReplication } from "@/components/database/mongo/api"
import { uptimeWords } from "@/components/database/mongo/performance/samples"
import type { MongoMember } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

/** How far behind the primary a member is before the figure is a reading worth a hue. */
const LAG_SECONDS = 10

/** A member's state as a reading: the two working states are fine, an unhealthy one is not. */
function MemberState({ member }: { member: MongoMember }) {
  const word = member.state.charAt(0) + member.state.slice(1).toLowerCase()
  if (!member.health) return <Status tone="danger" label={`${word}, not answering`} />
  if (member.state === "PRIMARY" || member.state === "SECONDARY" || member.state === "ARBITER") {
    return <Status tone="running" label={word} />
  }
  return <Status tone="warning" label={word} />
}

/** How far a member is behind the primary, in words: for a row drawn down rather than across. */
function lagWords(member: MongoMember): string {
  if (member.lagSeconds === null) return "lag not known"
  return member.lagSeconds < 1 ? "in step" : `${duration(member.lagSeconds)} behind`
}

/**
 * The members of the replica set, as this member sees them, and how far back
 * the replication log reaches.
 *
 * A secondary's lag is how far its last applied write is behind the
 * primary's; the log's window is how long a member can be away and still
 * catch up from it rather than starting over.
 *
 * The members are a table where the page is wide enough for its six columns.
 * Under that — a phone — each member is drawn down instead of across, and
 * nothing is dropped.
 */
export function ReplicationView({ mongo }: { mongo: Mongo }) {
  const { id } = mongo
  const replication = usePoll((signal) => mongoReplication(id, signal), 10_000, [id])
  const data = replication.data
  const [frame, width] = useColumnWidth()
  const wide = width === 0 || width >= 640

  // The measured frame is drawn from the first render on, whatever is in it:
  // its width is read once it is on the page, and that is before the members are.
  if (replication.error && !data) {
    return (
      <div ref={frame}>
        <ReadError error={replication.error} onRetry={replication.refresh} />
      </div>
    )
  }
  if (!data) {
    return (
      <div ref={frame}>
        <LoadingPanel plain rows={4} />
      </div>
    )
  }
  if (!data.replicaSet) {
    return (
      <div ref={frame}>
        <EmptyNote className="py-10">
          This server is not a member of a replica set
          {data.reason ? `: ${data.reason}.` : "."}
        </EmptyNote>
      </div>
    )
  }

  const oplog = data.oplog
  const used = oplog && oplog.sizeBytes > 0 ? (oplog.usedBytes / oplog.sizeBytes) * 100 : undefined

  return (
    <div ref={frame} className="space-y-8">
      {replication.error && (
        <p role="status" className="text-hint text-warning">
          The replica set could not be read again just now: {replication.error.message}
        </p>
      )}
      <Panel plain>
        <PanelHeader
          title={`Members of ${data.setName ?? "the replica set"}`}
          actions={
            data.myState && (
              <span className="text-hint text-muted-foreground">
                this server is {data.myState.toLowerCase()}
              </span>
            )
          }
        />
        <PanelBody flush className="group-data-[plain]/panel:-mx-4">
          {!wide ? (
            <ul className="divide-y divide-hairline">
              {data.members.map((member) => (
                <li key={member.id} data-slot="mongo-member" className="space-y-1 px-4 py-2.5">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 truncate font-mono text-xs font-medium">
                      {member.name}
                    </span>
                    {member.self && <Tag>this server</Tag>}
                    <span className="ml-auto flex shrink-0">
                      <MemberState member={member} />
                    </span>
                  </div>
                  {member.message && (
                    <p className="truncate text-hint text-muted-foreground">{member.message}</p>
                  )}
                  <p className="numeric text-hint text-muted-foreground">
                    <span
                      className={
                        member.lagSeconds !== null && member.lagSeconds >= LAG_SECONDS
                          ? "text-warning"
                          : undefined
                      }
                    >
                      {lagWords(member)}
                    </span>
                    {member.self ? "" : ` · ping ${member.pingMs.toLocaleString("en-US")} ms`}
                    {` · up ${uptimeWords(member.uptime)}`}
                    {member.syncSource ? ` · copies from ${member.syncSource}` : ""}
                  </p>
                </li>
              ))}
            </ul>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Member</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="text-right">Behind the primary</TableHead>
                  <TableHead className="text-right">Ping</TableHead>
                  <TableHead className="text-right">Up for</TableHead>
                  <TableHead className="max-lg:hidden">Copies from</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.members.map((member) => (
                  <TableRow key={member.id} data-slot="mongo-member">
                    <TableCell>
                      <span className="flex min-w-0 items-center gap-2">
                        <span className="truncate font-mono font-medium">{member.name}</span>
                        {member.self && <Tag>this server</Tag>}
                      </span>
                      {member.message && (
                        <p className="truncate text-hint text-muted-foreground">{member.message}</p>
                      )}
                    </TableCell>
                    <TableCell>
                      <span className="flex">
                        <MemberState member={member} />
                      </span>
                    </TableCell>
                    <TableCell
                      className={
                        member.lagSeconds !== null && member.lagSeconds >= LAG_SECONDS
                          ? "numeric text-right text-warning"
                          : "numeric text-right"
                      }
                      title={
                        member.optimeDate
                          ? `Last write applied ${relativeTime(member.optimeDate)}`
                          : undefined
                      }
                    >
                      {member.lagSeconds === null
                        ? "—"
                        : member.lagSeconds < 1
                          ? "in step"
                          : duration(member.lagSeconds)}
                    </TableCell>
                    <TableCell className="numeric text-right text-muted-foreground">
                      {member.self ? "—" : `${member.pingMs.toLocaleString("en-US")} ms`}
                    </TableCell>
                    <TableCell className="numeric text-right text-muted-foreground">
                      {uptimeWords(member.uptime)}
                    </TableCell>
                    <TableCell className="font-mono text-muted-foreground max-lg:hidden">
                      {member.syncSource || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="Replication log" />
        <PanelBody>
          {!oplog ? (
            <p className="text-body text-muted-foreground">
              The log&rsquo;s size and reach could not be read: the account this dashboard connects
              as may not read the <span className="font-mono">local</span> database.
            </p>
          ) : (
            <div className="max-w-2xl space-y-3">
              <MetricStrip>
                <Metric
                  label="Reaches back"
                  value={duration(oplog.windowSeconds)}
                  hint={oplog.first ? `to ${relativeTime(oplog.first)}` : undefined}
                />
                <Metric
                  label="Holds"
                  value={bytes(oplog.usedBytes)}
                  hint={`of ${bytes(oplog.sizeBytes)}`}
                />
                <Metric label="Newest entry" value={oplog.last ? relativeTime(oplog.last) : "—"} />
              </MetricStrip>
              {used !== undefined && <Meter value={used} label="How full the replication log is" />}
            </div>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
