"use client"

import { relativeTime, timestamp } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { ActionMark, ActionName, actorName, keyTitle, keyWords } from "@/components/audit/marks"
import { MethodWord, RequestPath, StatusCode } from "@/components/deploy/request-marks"
import { Detail, DetailList } from "@/components/page"
import { Address } from "@/components/security/marks"
import { Status } from "@/components/status-dot"

/**
 * The outcome as a reading: a dot and the status code in its family's colour,
 * the way the request log draws one, with the word for a failure beside it —
 * "403 Forbidden" says the capability check refused it before the row is
 * opened. Red arrives only here, attached to the code that failed, rather
 * than washed across the whole row. An entry the dashboard wrote for itself
 * carries no code, and says whether it worked in a word.
 */
export function EntryResult({ entry, className }: { entry: AuditEntry; className?: string }) {
  return (
    <Status
      tone={entry.success ? "running" : "danger"}
      label={
        entry.status ? (
          <StatusCode status={entry.status} word={!entry.success} />
        ) : entry.success ? (
          "done"
        ) : (
          "failed"
        )
      }
      className={className}
    />
  )
}

/**
 * One entry, whole: everything the trail keeps about it, with nothing cut to
 * fit a column. A row of the trail is one line, so a path longer than its
 * cell, the method, the key's record number and a session's id are read here,
 * in the popover the row's action opens.
 */
export function EntryDetail({ entry }: { entry: AuditEntry }) {
  const key = keyWords(entry)
  const record = keyTitle(entry)
  return (
    <div className="min-w-0 space-y-3">
      <div className="flex min-w-0 items-center gap-3">
        <ActionMark action={entry.action} />
        <div className="min-w-0 flex-1">
          <ActionName action={entry.action} className="block text-xs" />
          <p className="truncate text-hint text-muted-foreground">
            <span className="numeric">{timestamp(entry.ts)}</span> · {relativeTime(entry.ts)}
          </p>
        </div>
        <EntryResult entry={entry} className="shrink-0" />
      </div>
      <DetailList className="border-t border-hairline pt-3">
        <Detail label="Who">
          {actorName(entry)}
          {entry.role && <span className="text-muted-foreground"> · {entry.role}</span>}
        </Detail>
        {key && (
          <Detail label="Through">
            {key}
            {record && <span className="text-muted-foreground"> · {record}</span>}
          </Detail>
        )}
        {entry.ip && (
          <Detail label="From">
            <Address ip={entry.ip} className="max-w-full" />
          </Detail>
        )}
        {entry.path && (
          <Detail label="Request" className="font-mono break-all">
            <MethodWord method={entry.method} />{" "}
            <RequestPath path={entry.path} className="whitespace-normal" />
          </Detail>
        )}
        {entry.target && (
          <Detail label="Target" className="font-mono break-all">
            {entry.target}
          </Detail>
        )}
        {entry.detail && (
          <Detail label="Detail" className="break-words">
            {entry.detail}
          </Detail>
        )}
      </DetailList>
    </div>
  )
}
