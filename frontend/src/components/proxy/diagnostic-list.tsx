"use client"

import { useEffect, useRef } from "react"
import type { ProxyDiagnostic, ProxyNameClaim } from "@/lib/types"
import { RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { diagnosticVerdict } from "@/components/proxy/engine-lifecycle"

/** A file and a line the config editor can open: a diagnostic's, or a claim's. */
export type ProxyPlace = { file?: string; line?: number }

/**
 * The engine's config test, a line per thing it said: its level, its words,
 * and the file and line it names, with the file one press away where the
 * config editor may open it. nginx prints all of this as one block whose
 * file and line sit at the end of each line, past where a phone cuts it.
 *
 * A conflicting server name is the warning nginx names no file for; where the
 * server placed it, the line lists the sites that claim the name, the one
 * nginx serves it from and each it ignored, each with its own way to the file.
 */
export function DiagnosticList({
  diagnostics,
  canOpen,
  returnTo,
  onOpen,
}: {
  diagnostics: ProxyDiagnostic[]
  /** Whether the editor may open this file; a file outside the proxy's directories is only named. */
  canOpen: (file: string | undefined) => boolean
  /** The place whose file was just open, whose button takes the keyboard back. */
  returnTo?: ProxyPlace
  onOpen: (place: ProxyPlace) => void
}) {
  const returned = useRef<HTMLButtonElement>(null)
  // A child's effect runs before its dialog's, and the dialog's focus scope
  // leaves focus where it finds it inside; otherwise it takes the first
  // control, which need not be the line the reader left from.
  useEffect(() => {
    returned.current?.focus()
  }, [])
  return (
    <RowList aria-label="What the test said">
      {diagnostics.map((d, index) => (
        <li key={`${index}:${d.file ?? ""}:${d.line ?? ""}`} className="min-w-0 space-y-2 py-2.5">
          <div className="flex min-w-0 flex-wrap items-start justify-between gap-x-3 gap-y-1.5">
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
              <Button
                ref={d === returnTo ? returned : undefined}
                size="xs"
                variant="outline"
                onClick={() => onOpen(d)}
              >
                {d.line ? `Open at line ${d.line}` : "Open file"}
              </Button>
            )}
          </div>
          {d.claims && d.claims.length > 0 && (
            <ul
              aria-label="Sites that claim this name"
              className="space-y-1.5 border-l border-hairline pl-3"
            >
              {d.claims.map((claim) => (
                <Claim
                  key={`${claim.file}:${claim.line}`}
                  claim={claim}
                  canOpen={canOpen(claim.file)}
                  buttonRef={claim === returnTo ? returned : undefined}
                  onOpen={() => onOpen(claim)}
                />
              ))}
            </ul>
          )}
        </li>
      ))}
    </RowList>
  )
}

/**
 * One site claiming a conflicting name. Its button says the same words as a
 * diagnostic's, and its name says whose line, since two claims are often on
 * the same line of two files.
 */
function Claim({
  claim,
  canOpen,
  buttonRef,
  onOpen,
}: {
  claim: ProxyNameClaim
  canOpen: boolean
  buttonRef?: React.Ref<HTMLButtonElement>
  onOpen: () => void
}) {
  const name = claim.file.split("/").pop() ?? claim.file
  return (
    <li className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
      <p className="min-w-0 flex-[1_1_14rem] text-hint">
        <span className={claim.ignored ? "font-medium text-warning" : "text-muted-foreground"}>
          {claim.ignored ? "ignored in" : "served by"}
        </span>{" "}
        <span className="font-mono break-all text-muted-foreground">
          {claim.file}:{claim.line}
        </span>
      </p>
      {canOpen && (
        <Button
          ref={buttonRef}
          size="xs"
          variant="outline"
          aria-label={`Open at line ${claim.line} of ${name}`}
          onClick={onOpen}
        >
          Open at line {claim.line}
        </Button>
      )}
    </li>
  )
}
