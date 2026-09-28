"use client"

import type { ProxyDiagnostic } from "@/lib/types"
import { RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { diagnosticVerdict } from "@/components/proxy/engine-lifecycle"

/**
 * The engine's config test, a line per thing it said: its level, its words,
 * and the file and line it names, with the file one press away where the
 * config editor may open it. nginx prints all of this as one block whose
 * file and line sit at the end of each line, past where a phone cuts it.
 */
export function DiagnosticList({
  diagnostics,
  canOpen,
  onOpen,
}: {
  diagnostics: ProxyDiagnostic[]
  /** Whether the editor may open this file; a file outside the proxy's directories is only named. */
  canOpen: (file: string | undefined) => boolean
  onOpen: (diagnostic: ProxyDiagnostic) => void
}) {
  return (
    <RowList aria-label="What the test said">
      {diagnostics.map((d, index) => (
        <li
          key={`${index}:${d.file ?? ""}:${d.line ?? ""}`}
          className="flex min-w-0 flex-wrap items-start justify-between gap-x-3 gap-y-1.5 py-2.5"
        >
          {/* Wide enough for a path before the button shares its line; on a
              phone the button goes under it rather than squeezing it. */}
          <div className="min-w-0 flex-[1_1_16rem] space-y-1">
            <p className="flex min-w-0 items-baseline gap-2 text-body">
              <Status verdict={diagnosticVerdict(d.level)} label={d.level} />
              <span className="min-w-0 break-words">{d.message}</span>
            </p>
            {d.file && (
              <p className="font-mono text-hint break-all text-muted-foreground">
                {d.file}
                {d.line ? `:${d.line}` : ""}
              </p>
            )}
          </div>
          {canOpen(d.file) && (
            <Button size="xs" variant="outline" onClick={() => onOpen(d)}>
              {d.line ? `Open at line ${d.line}` : "Open file"}
            </Button>
          )}
        </li>
      ))}
    </RowList>
  )
}
