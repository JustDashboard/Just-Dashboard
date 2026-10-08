"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { ApiError, put, refusedIndex } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { HostRecords } from "@/lib/types"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Row = { key: number; address: string; names: string }

const words = (text: string) => text.split(/[\s,]+/).filter(Boolean)
const rowsOf = (records: HostRecords["managed"]): Row[] =>
  records.map((r, key) => ({ key, address: r.address, names: r.names.join(" ") }))
const same = (a: Row[], b: Row[]) =>
  a.length === b.length &&
  a.every(
    (r, i) =>
      r.address.trim() === b[i].address.trim() &&
      words(r.names).join(" ") === words(b[i].names).join(" "),
  )

/**
 * The host's own records: the block of `/etc/hosts` the dashboard owns, as a
 * list of address and names that Save writes back whole, and everything else
 * in the file under it as a table that is only read. The server edits the
 * file as bytes around its marker comments, so what is not here is carried
 * across untouched; a file whose markers cannot be trusted is shown read-only
 * with the reason.
 *
 * A row with nothing in it is dropped on save rather than refused — it is the
 * one just added and not used — while a row with half of itself is the
 * mistake to catch: the field that is missing says so beside it.
 */
export function HostRecordsEditor({
  records,
  onSaved,
}: {
  records: HostRecords
  onSaved: () => void
}) {
  const { can } = useAuth()
  const saved = rowsOf(records.managed)
  const [edits, setEdits] = useState<{ rows: Row[]; next: number }>()
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<{ row?: number; message: string }>()
  const rows = edits?.rows ?? saved
  const next = edits?.next ?? saved.length
  // A half-written row is a mistake; an empty one is the one just added.
  const used = rows.filter((r) => r.address.trim() || r.names.trim())
  const dirty = edits !== undefined && !same(used, saved)
  const locked = !can("system.admin") || Boolean(records.problem)

  const update = (nextRows: Row[], nextKey = next) => {
    setEdits({ rows: nextRows, next: nextKey })
    setRefusal(undefined)
  }
  const change = (key: number, patch: Partial<Row>) =>
    update(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)))

  const incomplete = (r: Row) => !r.address.trim() || words(r.names).length === 0
  const firstBad = used.find(incomplete)

  const save = async () => {
    if (firstBad) {
      setRefusal({
        row: firstBad.key,
        message: firstBad.address.trim() ? "Give it a name." : "Give it an address.",
      })
      return
    }
    setBusy(true)
    setRefusal(undefined)
    try {
      await put("/network/dns/hosts", {
        records: used.map((r) => ({ address: r.address.trim(), names: words(r.names) })),
      })
      notify.success("Host records saved")
      setEdits(undefined)
      onSaved()
    } catch (err) {
      const index = refusedIndex(err instanceof ApiError ? err.field : undefined, "records")
      setRefusal({
        row: index === undefined ? undefined : used[index]?.key,
        message: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section
      title="Host records"
      actions={
        <span className="flex flex-wrap items-center gap-3">
          <span className="numeric text-hint text-muted-foreground">
            {plural(records.managed.length, "record")} made here in{" "}
            <span className="font-mono">{records.path}</span>
          </span>
          <Button
            size="xs"
            variant="outline"
            disabled={locked || busy}
            onClick={() => update([...rows, { key: next, address: "", names: "" }], next + 1)}
          >
            <Plus aria-hidden />
            Add record
          </Button>
        </span>
      }
    >
      <div className="flex min-w-0 flex-col gap-6">
        <div className="flex min-w-0 flex-col gap-4">
          {records.problem && (
            <Notice title="This file cannot be edited from here" tone="warning">
              {records.problem}
            </Notice>
          )}
          {rows.length === 0 ? (
            <p className="py-1 text-body text-muted-foreground">
              No records made here. A record sends a name to an address for this server alone, ahead
              of any DNS server.
            </p>
          ) : (
            <ul className="flex flex-col gap-3">
              {rows.map((row, index) => {
                const bad = refusal?.row === row.key
                const missingAddress = bad && !row.address.trim()
                const missingNames = bad && !missingAddress && words(row.names).length === 0
                return (
                  <li
                    key={row.key}
                    className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[minmax(0,15rem)_minmax(0,1fr)_auto] sm:items-start"
                  >
                    <Input
                      aria-label={`Address of record ${index + 1}`}
                      aria-invalid={missingAddress || undefined}
                      value={row.address}
                      disabled={locked}
                      spellCheck={false}
                      autoComplete="off"
                      placeholder="192.0.2.10"
                      className="col-span-2 font-mono sm:col-span-1"
                      onChange={(event) => change(row.key, { address: event.target.value })}
                    />
                    <Input
                      aria-label={`Names of record ${index + 1}`}
                      aria-invalid={missingNames || undefined}
                      value={row.names}
                      disabled={locked}
                      spellCheck={false}
                      autoComplete="off"
                      placeholder="nas.lan nas"
                      className="font-mono"
                      onChange={(event) => change(row.key, { names: event.target.value })}
                    />
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      aria-label={`Remove record ${index + 1}`}
                      disabled={locked}
                      onClick={() => update(rows.filter((r) => r.key !== row.key))}
                    >
                      <Trash aria-hidden />
                    </Button>
                    {bad && (
                      <p
                        role="alert"
                        className="animate-rise text-hint text-destructive sm:col-span-3"
                      >
                        {refusal.message}
                      </p>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
          {refusal && refusal.row === undefined && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {refusal.message}
            </p>
          )}
          <div className="flex items-center gap-2">
            <Button onClick={() => void save()} disabled={!dirty || locked} pending={busy}>
              Save
            </Button>
            {dirty && (
              <Button variant="ghost" onClick={() => setEdits(undefined)} disabled={busy}>
                Discard
              </Button>
            )}
          </div>
        </div>

        {records.other.length > 0 && (
          <Panel>
            <PanelHeader
              title="Everything else in the file"
              actions={
                <span className="numeric text-hint text-muted-foreground">
                  {plural(records.other.length, "entry", "entries")} · read only
                </span>
              }
            />
            <PanelBody flush>
              <Table containerClassName="max-h-80">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16 text-right">Line</TableHead>
                    <TableHead>Address</TableHead>
                    <TableHead>Names</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {records.other.map((line) => (
                    <TableRow key={line.line}>
                      <TableCell className="numeric text-right font-mono text-muted-foreground">
                        {line.line}
                      </TableCell>
                      <TableCell className="font-mono">{line.address}</TableCell>
                      <TableCell className="font-mono whitespace-normal">
                        {line.names.join(" ")}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </PanelBody>
          </Panel>
        )}
      </div>
    </Section>
  )
}
