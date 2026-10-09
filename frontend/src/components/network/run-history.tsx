"use client"

import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import {
  diagnosticReading,
  formatMetric,
  historyKeys,
  readDiagnosticHistory,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { NetworkReadWarning } from "./read-warning"

/**
 * Every retained run of exactly this request, oldest first, with its outcome
 * and the numbers the tool measured — loss and jitter for ping, hops for a
 * traceroute, days left on a certificate. It reads saved runs only.
 */
export function RunHistory({ run }: { run: DiagnosticRun }) {
  const history = usePoll(
    async (signal) =>
      readDiagnosticHistory(
        await get<unknown>(
          `/network/diagnostics/${encodeURIComponent(run.id)}/history`,
          undefined,
          signal,
        ),
      ),
    0,
    [run.id, run.status],
  )
  const keys = history.data ? historyKeys(history.data) : []
  return (
    <Panel plain aria-label="Run history">
      <PanelHeader title="History of this request" />
      <PanelBody className="space-y-3">
        <NetworkReadWarning
          error={history.data ? history.error : undefined}
          refresh={history.refresh}
          lastSuccess={history.lastSuccess}
          reading="run history"
        />
        {!history.data && history.error && (
          <ErrorState error={history.error} onRetry={history.refresh} />
        )}
        {!history.data && !history.error && <LoadingPanel plain />}
        {history.data && history.data.points.length < 2 && (
          <p className="text-body text-muted-foreground">
            Only this run is retained for the request. Rerun it to build a history.
          </p>
        )}
        {history.data && history.data.points.length > 0 && (
          <Table containerClassName="max-h-80 rounded-md border border-hairline">
            <TableHeader>
              <TableRow>
                <TableHead>Run</TableHead>
                <TableHead>Outcome</TableHead>
                {keys.map((key) => (
                  <TableHead key={key.key}>{key.label}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.data.points.map((point) => (
                <TableRow key={point.id} aria-current={point.id === run.id ? "true" : undefined}>
                  <TableCell>
                    <time dateTime={point.createdAt}>
                      {new Date(point.createdAt).toLocaleString()}
                    </time>
                    {point.id === run.id && (
                      <span className="ml-1.5 text-muted-foreground">(this run)</span>
                    )}
                  </TableCell>
                  <TableCell>{diagnosticReading(point).label}</TableCell>
                  {keys.map((key) => {
                    const metric = point.metrics.find((entry) => entry.key === key.key)
                    return (
                      <TableCell key={key.key} className="numeric">
                        {metric ? formatMetric(metric) : "—"}
                      </TableCell>
                    )
                  })}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {history.data?.limitations.map((line) => (
          <p key={line} className="text-hint text-muted-foreground">
            {line}
          </p>
        ))}
      </PanelBody>
    </Panel>
  )
}
