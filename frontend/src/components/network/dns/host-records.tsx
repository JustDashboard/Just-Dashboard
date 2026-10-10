"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { ApiError, get, post, put, refusedIndex } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  HostNameResolution,
  HostRecordIssue,
  HostRecords,
  HostRecordsPreview,
  HostResolutionEvidence,
} from "@/lib/types"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { issueMatters, issuesByRecord } from "@/components/network/dns/resolvers"
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
  // The overlaps of the rows as last previewed, keyed to the rows they were
  // previewed for; an edit clears them, so a stale warning never sits beside
  // a row that no longer says it.
  const [preview, setPreview] = useState<{ keys: number[]; value: HostRecordsPreview }>()
  const [evidence, setEvidence] = useState<HostResolutionEvidence>()
  const [checking, setChecking] = useState(false)
  const [evidenceError, setEvidenceError] = useState<string>()
  const rows = edits?.rows ?? saved
  const next = edits?.next ?? saved.length
  // A half-written row is a mistake; an empty one is the one just added.
  const used = rows.filter((r) => r.address.trim() || r.names.trim())
  const dirty = edits !== undefined && !same(used, saved)
  const locked = !can("system.admin") || Boolean(records.problem)

  const update = (nextRows: Row[], nextKey = next) => {
    setEdits({ rows: nextRows, next: nextKey })
    setRefusal(undefined)
    setPreview(undefined)
  }
  const change = (key: number, patch: Partial<Row>) =>
    update(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)))

  const incomplete = (r: Row) => !r.address.trim() || words(r.names).length === 0
  const firstBad = used.find(incomplete)

  const payload = used.map((r) => ({ address: r.address.trim(), names: words(r.names) }))
  const refuseAt = (err: unknown) => {
    const index = refusedIndex(err instanceof ApiError ? err.field : undefined, "records")
    setRefusal({
      row: index === undefined ? undefined : used[index]?.key,
      message: err instanceof Error ? err.message : String(err),
    })
  }

  const checkResolution = async () => {
    setChecking(true)
    setEvidenceError(undefined)
    try {
      setEvidence(await get<HostResolutionEvidence>("/network/dns/hosts/resolution"))
    } catch (err) {
      setEvidenceError(err instanceof Error ? err.message : String(err))
    } finally {
      setChecking(false)
    }
  }

  // Previewing names every overlap against the file as it is now; Save
  // previews first and stops on one that changes which address a program
  // gets, until the reader saves again having seen it.
  const previewRecords = async () => {
    const value = await post<HostRecordsPreview>("/network/dns/hosts/preview", { records: payload })
    setPreview({ keys: used.map((r) => r.key), value })
    return value
  }
  const reviewed = preview && preview.keys.join(",") === used.map((r) => r.key).join(",")

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
      if (!reviewed) {
        const value = await previewRecords()
        if (value.issues.some(issueMatters)) return
      }
      await put("/network/dns/hosts", { records: payload })
      notify.success("Host records saved")
      setEdits(undefined)
      setPreview(undefined)
      onSaved()
      if (payload.length > 0) void checkResolution()
    } catch (err) {
      refuseAt(err)
    } finally {
      setBusy(false)
    }
  }
  const byRecord: Map<number, HostRecordIssue[]> = preview
    ? issuesByRecord(preview.value.issues)
    : new Map()
  const rowIssues = (key: number) => {
    const index = preview?.keys.indexOf(key) ?? -1
    return index >= 0 ? (byRecord.get(index) ?? []) : []
  }
  const blocking = preview?.value.issues.some(issueMatters) ?? false

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
                    {rowIssues(row.key).map((issue, at) => (
                      <p
                        key={`${issue.kind}:${issue.name}:${at}`}
                        className={
                          issueMatters(issue)
                            ? "col-span-2 text-hint text-warning sm:col-span-3"
                            : "col-span-2 text-hint text-muted-foreground sm:col-span-3"
                        }
                      >
                        <span className="font-mono">{issue.name}</span>: {issue.detail}
                      </p>
                    ))}
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
          {preview && (
            <div role="status" className="flex min-w-0 flex-col gap-1">
              <p className="text-body">
                {preview.value.issues.length === 0
                  ? "No overlaps with each other or with the rest of the file."
                  : `${plural(preview.value.issues.length, "overlap")} with the records or the rest of the file.`}
                {blocking && " Save again to write them as they are."}
              </p>
              <p className="numeric text-hint text-muted-foreground">
                {plural(preview.value.added.length, "record")} added ·{" "}
                {plural(preview.value.removed.length, "record")} removed
              </p>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={() => void save()} disabled={!dirty || locked} pending={busy}>
              {blocking && reviewed ? "Save anyway" : "Save"}
            </Button>
            {dirty && !locked && (
              <Button
                variant="outline"
                onClick={() => void previewRecords().catch(refuseAt)}
                disabled={busy || Boolean(firstBad)}
              >
                Preview overlaps
              </Button>
            )}
            {dirty && (
              <Button
                variant="ghost"
                onClick={() => {
                  setEdits(undefined)
                  setPreview(undefined)
                }}
                disabled={busy}
              >
                Discard
              </Button>
            )}
            {records.managed.length > 0 && !dirty && (
              <Button variant="outline" onClick={() => void checkResolution()} pending={checking}>
                Check local resolution
              </Button>
            )}
          </div>
          {evidenceError && (
            <p role="alert" className="text-body text-destructive">
              {evidenceError}
            </p>
          )}
          {evidence && <ResolutionEvidence evidence={evidence} />}
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

const RESOLUTION: Record<
  HostNameResolution["state"],
  { label: string; tone: "running" | "warning" | "notice" }
> = {
  matches: { label: "Resolves as written", tone: "running" },
  includes: { label: "Another address first", tone: "warning" },
  differs: { label: "Resolves elsewhere", tone: "warning" },
  unresolved: { label: "Not resolved", tone: "warning" },
  unknown: { label: "Not checked", tone: "notice" },
}

/**
 * What the host's own NSS answers for each managed name, through
 * nsswitch.conf the way any program on the host would ask: the address it
 * returns first for each family, against the address written here.
 */
function ResolutionEvidence({ evidence }: { evidence: HostResolutionEvidence }) {
  return (
    <section aria-label="Local resolution" className="flex min-w-0 flex-col gap-2">
      <p className="text-hint text-muted-foreground">
        hosts: <span className="font-mono">{evidence.hosts}</span> · checked{" "}
        {new Date(evidence.checkedAt).toLocaleTimeString()}
        {evidence.omitted > 0 && ` · ${plural(evidence.omitted, "name")} past the bound not asked`}
      </p>
      <ul className="flex min-w-0 flex-col divide-y divide-hairline">
        {evidence.names.map((n) => (
          <li key={n.name} className="flex min-w-0 flex-col gap-1 py-2">
            <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3">
              <p className="min-w-0 font-mono text-body">{n.name}</p>
              <Status tone={RESOLUTION[n.state].tone} label={RESOLUTION[n.state].label} />
            </div>
            <p className="text-hint break-words text-muted-foreground">
              written <span className="font-mono">{n.configured.join(" ")}</span>
              {n.ipv4.length + n.ipv6.length > 0 && (
                <>
                  {" "}
                  · answered <span className="font-mono">{[...n.ipv4, ...n.ipv6].join(" ")}</span>
                </>
              )}{" "}
              · {n.detail}
            </p>
          </li>
        ))}
      </ul>
      {evidence.limitations.map((line) => (
        <p key={line} className="text-hint text-muted-foreground">
          {line}
        </p>
      ))}
    </section>
  )
}
