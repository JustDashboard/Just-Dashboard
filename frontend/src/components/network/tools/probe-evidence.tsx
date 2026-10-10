"use client"

import Link from "next/link"
import { FindingList } from "@/components/finding-list"
import { Detail, DetailList, Metric, MetricStrip } from "@/components/page"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  basisLabel,
  formatMetric,
  stageTone,
  type DiagnosticResult,
} from "@/lib/network-diagnostics"

/**
 * The structured half of a diagnostic: the stages it went through, what it
 * found and who acts on it, the facts with what each rests on, its tables and
 * numbers, and where to go next. The same renderer serves a quick run and a
 * saved one, so the two never read differently.
 */
export function ProbeEvidence({ result }: { result: DiagnosticResult }) {
  return (
    <div className="min-w-0 space-y-5">
      {result.summary && <p className="text-body leading-relaxed">{result.summary}</p>}

      {Boolean(result.stages?.length) && (
        <section aria-label="Checks" className="space-y-2">
          <h3 className="eyebrow">Checks</h3>
          <ol className="divide-y divide-hairline">
            {result.stages!.map((stage) => (
              <li
                key={stage.id}
                data-status={stage.status}
                className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-1.5"
              >
                <span className="flex min-w-0 items-center gap-2">
                  <StatusDot tone={stageTone(stage.status)} />
                  <span className="text-body font-medium">{stage.label}</span>
                  <span className="text-hint text-muted-foreground">{stage.status}</span>
                </span>
                {stage.detail && (
                  <span className="min-w-0 flex-1 text-hint break-words text-muted-foreground">
                    {stage.detail}
                  </span>
                )}
                {stage.duration && (
                  <span className="numeric text-hint text-muted-foreground">{stage.duration}</span>
                )}
              </li>
            ))}
          </ol>
        </section>
      )}

      {Boolean(result.findings?.length) && (
        <section aria-label="Findings" className="space-y-2">
          <h3 className="eyebrow">Findings</h3>
          <FindingList
            findings={result.findings!.map((finding) => ({
              id: finding.id,
              level: finding.level,
              title: finding.title,
              detail: finding.detail,
              advice: finding.action,
              meta: finding.owner,
              extra: (
                <>
                  {finding.owner && <p>Owner: {finding.owner}</p>}
                  {finding.href && (
                    <Link href={finding.href} className="underline underline-offset-4">
                      Open where this is fixed
                    </Link>
                  )}
                </>
              ),
            }))}
          />
        </section>
      )}

      {Boolean(result.metrics?.length) && (
        <MetricStrip aria-label="Measurements">
          {result.metrics!.map((metric) => (
            <Metric key={metric.key} label={metric.label} value={formatMetric(metric)} />
          ))}
        </MetricStrip>
      )}

      {Boolean(result.facts?.length) && (
        <DetailList aria-label="Facts">
          {result.facts!.map((fact, index) => (
            <Detail key={`${index}:${fact.label}`} label={fact.label}>
              <span className="break-words">{fact.value}</span>
              {fact.basis && (
                <Tag className="ml-1.5 align-middle" title="How this value was obtained">
                  {basisLabel(fact.basis)}
                </Tag>
              )}
            </Detail>
          ))}
        </DetailList>
      )}

      {result.tables?.map((table) => (
        <section key={table.id} aria-label={table.title} className="min-w-0 space-y-2">
          <h3 className="eyebrow">{table.title}</h3>
          <Table containerClassName="max-h-80 rounded-md border border-hairline">
            <TableHeader>
              <TableRow>
                {table.columns.map((column) => (
                  <TableHead key={column}>{column}</TableHead>
                ))}
                {table.rowLinks?.some(Boolean) && (
                  <TableHead>
                    <span className="sr-only">Open</span>
                  </TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {table.rows.map((row, index) => (
                <TableRow key={index}>
                  {row.map((cell, column) => (
                    <TableCell
                      key={column}
                      className="max-w-[24rem] font-mono break-all whitespace-normal"
                    >
                      {cell}
                    </TableCell>
                  ))}
                  {table.rowLinks?.some(Boolean) && (
                    <TableCell>
                      {table.rowLinks[index] && (
                        <Link
                          href={table.rowLinks[index]}
                          className="underline underline-offset-4"
                          aria-label={`Open ${row[0]} ${row[1] ?? ""}`.trim()}
                        >
                          Open
                        </Link>
                      )}
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {table.note && <p className="text-hint text-muted-foreground">{table.note}</p>}
        </section>
      ))}

      {Boolean(result.links?.length) && (
        <div className="flex flex-wrap gap-2" aria-label="Next steps">
          {result.links!.map((link) => (
            <Button key={link.href + link.label} asChild size="sm" variant="outline">
              <Link href={link.href}>{link.label}</Link>
            </Button>
          ))}
        </div>
      )}

      {Boolean(result.limitations?.length) && (
        <ul className="space-y-1" aria-label="Limits of this evidence">
          {result.limitations!.map((line) => (
            <li key={line} className="text-hint text-muted-foreground">
              {line}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
