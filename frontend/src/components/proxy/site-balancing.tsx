"use client"

import { relativeTime } from "@/lib/format"
import type { PoolEvidence, UpstreamPool } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  balancingText,
  failuresText,
  memberCheck,
  memberRole,
  poolVerdict,
} from "@/components/proxy/upstream-pools"

/**
 * Where a site's requests go, as nginx spreads them: the pool's servers with
 * the check's reading of each and what nginx itself logged meeting them over
 * the last hour, or the single address whose spreading — if anything spreads
 * it — is somebody else's to see.
 */
export function SiteBalancing({
  pools,
  evidence,
}: {
  pools: UpstreamPool[]
  evidence: PoolEvidence | undefined
}) {
  if (pools.length === 0) return null
  return (
    <>
      {pools.map((pool) => (
        <PoolPanel
          key={`${pool.kind}:${pool.name ?? pool.members[0]?.address}`}
          pool={pool}
          evidence={evidence}
        />
      ))}
    </>
  )
}

function PoolPanel({ pool, evidence }: { pool: UpstreamPool; evidence: PoolEvidence | undefined }) {
  const verdict = poolVerdict(pool)
  const window = evidence
    ? `nginx's log since ${relativeTime(evidence.since)}${evidence.complete ? "" : " (partly read)"}`
    : "nginx's log"
  const title = pool.name ? `Balancing · ${pool.name}` : "Balancing"
  return (
    <Panel plain aria-label={title}>
      <PanelHeader title={title} actions={<Status tone={verdict.tone} label={verdict.label} />} />
      <PanelBody>
        <div className="min-w-0 space-y-3">
          <p className="text-body break-words">{balancingText(pool)}</p>
          {(pool.noLive ?? 0) > 0 && (
            <p className="text-body text-destructive">
              nginx had nowhere to send {pool.noLive} request{pool.noLive === 1 ? "" : "s"}: it had
              set every server aside.
            </p>
          )}
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[30%]">Server</TableHead>
                <TableHead className="w-[26%]">Role</TableHead>
                <TableHead className="w-[18%]">Check</TableHead>
                <TableHead>{window}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {pool.members.map((m) => {
                const check = memberCheck(m)
                const failures = failuresText(m)
                return (
                  <TableRow key={m.address}>
                    <TableCell className="font-mono wrap-anywhere">
                      {m.address}
                      {m.resolved && m.resolved.length > 0 && (
                        <span className="block text-muted-foreground">
                          resolves to {m.resolved.join(", ")}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="wrap-anywhere">{memberRole(m)}</TableCell>
                    <TableCell>
                      <Status tone={check.tone} label={check.label} />
                    </TableCell>
                    <TableCell className="wrap-anywhere">
                      {failures || (m.down ? "—" : "nothing logged")}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </PanelBody>
    </Panel>
  )
}
