"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { ArrowRight, Inspect, MagnifyingGlass } from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { get } from "@/lib/api"
import type { DbConnection, DbSearchResult } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * "This order id appears somewhere — which table?"
 *
 * It is the query a developer types by hand most often and enjoys least: a
 * dozen ad-hoc SELECTs, one per table they can remember, and the answer is
 * usually in the one they forgot. The server does it in one request, bounded so
 * that pointing it at a production database is a reasonable thing to do.
 *
 * Deliberately not polled and not run as you type. A schema-wide scan is a
 * question you ask on purpose, and one that fired on every keystroke would be
 * the most expensive control in the product.
 */
export function SearchTab({
  conn,
  schema,
  onOpenTable,
}: {
  conn: DbConnection
  schema: string
  onOpenTable?: (schema: string, table: string) => void
}) {
  const [needle, setNeedle] = useSessionState(`databases.${conn.id}.search.needle`, "")
  const [result, setResult] = useState<DbSearchResult | null>(null)
  const [ran, setRan] = useState("")
  const [busy, setBusy] = useState(false)

  const run = async () => {
    const q = needle.trim()
    if (!q) return
    setBusy(true)
    try {
      const res = await get<DbSearchResult>(`/databases/${conn.id}/search`, { q, schema })
      setResult(res)
      setRan(q)
    } catch (err) {
      notify.error("Search failed", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel plain className="animate-rise">
      <PanelHeader title="Find a value" />
      <PanelToolbar>
        <SearchInput
          placeholder="An id, an email, an order number…"
          value={needle}
          onChange={(e) => setNeedle(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && run()}
          containerClassName="sm:w-96"
        />
        <Button size="sm" onClick={run} disabled={busy || !needle.trim()} pending={busy}>
          <MagnifyingGlass className="size-3.5" />
          Search
        </Button>
        {result && (
          <span className="text-xs text-muted-foreground">
            {plural(result.matches.length, "match", "matches")} across{" "}
            {plural(result.tablesScanned, "table")}
          </span>
        )}
      </PanelToolbar>
      <PanelBody flush>
        {!result && !busy && (
          <EmptyState
            icon={Inspect}
            title="Search the whole schema"
            description="Every column of every table is compared as text, so an integer id and a uuid match as readily as a name. Views are skipped, and the scan is bounded so it stays safe to run against a live database."
          />
        )}
        {result && (
          <>
            {result.truncated && (
              <Notice tone="warning" className="mb-3" title="Results are incomplete">
                The scan stopped at its limit. Narrow the value or search a specific table from the
                Browse tab&apos;s filter row for the full picture.
              </Notice>
            )}
            {result.tablesSkipped && result.tablesSkipped.length > 0 && (
              <Notice tone="default" className="mb-3" title="Some tables were skipped">
                {result.tablesSkipped.join(", ")} could not be read — usually a permission on that
                table. Everything else was searched.
              </Notice>
            )}
            {result.matches.length === 0 ? (
              <EmptyState
                icon={Inspect}
                title={`No row contains “${ran}”`}
                description={`Searched ${plural(result.tablesScanned, "table")}.`}
              />
            ) : (
              <div className="-mx-4 min-w-0 overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-48">Table</TableHead>
                      <TableHead className="w-40">Column</TableHead>
                      <TableHead>Value</TableHead>
                      <TableHead className="w-24" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {result.matches.map((m, i) => (
                      <TableRow key={`${m.table}-${m.column}-${i}`}>
                        <TableCell className="font-mono">{m.table}</TableCell>
                        <TableCell className="font-mono text-muted-foreground">
                          {m.column || <Tag>row match</Tag>}
                        </TableCell>
                        <TableCell className="max-w-0">
                          <code
                            className="block truncate font-mono text-xs"
                            title={JSON.stringify(m.row, null, 2)}
                          >
                            {m.value}
                          </code>
                        </TableCell>
                        <TableCell className="text-right">
                          {onOpenTable && (
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => onOpenTable(m.schema, m.table)}
                            >
                              Open
                              <ArrowRight className="size-3.5" />
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
