"use client"

import { useDeferredValue, useMemo, useState } from "react"
import { RefreshClockwise, Warning } from "@/components/icons"
import { plural } from "@/lib/format"
import type { ProxyEffectiveConfig } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { SearchInput, Toolbar } from "@/components/page"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Segments } from "@/components/deploy/settings/segments"
import { useNow } from "@/components/deploy/vocabulary"
import { DiagnosticList } from "@/components/proxy/diagnostic-list"
import { openableFile, refusalOf } from "@/components/proxy/engine-lifecycle"
import {
  fileLines,
  MAX_MATCHES,
  readLabel,
  relativePath,
  searchEffective,
  type SearchMatch,
  type SearchMode,
} from "@/components/proxy/config-tree"

const MODES: { value: SearchMode; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "regex", label: "Regex" },
  { value: "directive", label: "Directive" },
]

const PLACEHOLDER: Record<SearchMode, string> = {
  text: "Text in what nginx loads",
  regex: "A regular expression",
  directive: "A directive, then words in it: listen 443",
}

/**
 * What nginx loads, as `nginx -T` prints it: every file in the order nginx
 * reads it, and a search across them by text, by regular expression, or by
 * directive — the last reading the configuration as nginx does, so a match
 * says which server and location it sits in. Each file and each match opens
 * the config editor, at the match's line.
 */
export function EffectiveConfigView({
  effective,
  root,
  onOpen,
}: {
  effective: PollState<ProxyEffectiveConfig>
  root: string
  onOpen: (path: string, line?: number) => void
}) {
  const [query, setQuery] = useSessionState("proxy.config.search", "")
  const [mode, setMode] = useSessionState<SearchMode>("proxy.config.mode", "text")
  const deferred = useDeferredValue(query)
  const config = effective.data
  const result = useMemo(
    () => (config ? searchEffective(config, deferred, mode) : undefined),
    [config, deferred, mode],
  )
  // What the poll held when Read again was pressed: until either changes,
  // the read is still out.
  const [asked, setAsked] = useState<{ data: unknown; error: unknown }>()
  const reading = asked !== undefined && asked.data === config && asked.error === effective.error
  const readAgain = () => {
    setAsked({ data: config, error: effective.error })
    effective.refresh()
  }
  const refusal = refusalOf(effective.error)

  return (
    <div className="min-w-0 space-y-5">
      <Toolbar>
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={PLACEHOLDER[mode]}
          aria-label="Search what nginx loads"
          aria-invalid={result?.error ? true : undefined}
          className="font-mono"
        />
        <Segments label="Search by" value={mode} options={MODES} onChange={setMode} />
        <span className="flex items-center gap-1 sm:ml-auto">
          {config && !reading && <ReadAt at={Date.parse(config.checkedAt)} />}
          <Button size="xs" variant="ghost" onClick={readAgain} pending={reading}>
            <RefreshClockwise />
            {reading ? "Reading…" : "Read again"}
          </Button>
        </span>
      </Toolbar>

      {refusal ? (
        // nginx prints what it loads only for a configuration that passes its
        // test, so a refusal is the test: each line where it points.
        <div className="space-y-3">
          <Notice tone="danger" icon={Warning} title="nginx refuses its configuration">
            With nothing it would load, there is nothing to show or search. Fix what the test found,
            then read again.
          </Notice>
          {(refusal.validation?.diagnostics ?? []).length > 0 && (
            <DiagnosticList
              diagnostics={refusal.validation?.diagnostics ?? []}
              canOpen={(file) => openableFile(file, [root])}
              onOpen={(place) => place.file && onOpen(place.file, place.line)}
            />
          )}
          {refusal.validation?.output && (
            <Disclosure quiet summary="nginx -T output">
              <Well className="max-h-64 break-words whitespace-pre-wrap">
                {refusal.validation.output}
              </Well>
            </Disclosure>
          )}
        </div>
      ) : effective.error ? (
        <ErrorState error={effective.error} onRetry={readAgain} />
      ) : !config ? (
        <LoadingRows rows={6} />
      ) : !deferred.trim() ? (
        <LoadOrder config={config} root={root} onOpen={onOpen} />
      ) : result?.error ? (
        <p role="alert" className="text-hint text-destructive">
          {result.error}
        </p>
      ) : result && result.total === 0 ? (
        <EmptyNote>Nothing nginx loads matches.</EmptyNote>
      ) : (
        result && (
          <div className="min-w-0 space-y-2">
            <GroupRule
              label={plural(result.total, "match", "matches")}
              detail={result.total > MAX_MATCHES ? `showing the first ${MAX_MATCHES}` : undefined}
            />
            <ChoiceList aria-label="Matches">
              {result.matches.map((match) => (
                <MatchRow
                  key={`${match.file}:${match.line}:${match.start}`}
                  match={match}
                  root={root}
                  onOpen={onOpen}
                />
              ))}
            </ChoiceList>
          </div>
        )
      )}
    </div>
  )
}

/** The files nginx loads, in the order it reads them, each opening in the editor. */
function LoadOrder({
  config,
  root,
  onOpen,
}: {
  config: ProxyEffectiveConfig
  root: string
  onOpen: (path: string) => void
}) {
  return (
    <div className="min-w-0 space-y-2">
      <GroupRule label="Read in this order" count={config.files.length} />
      <ChoiceList aria-label="Files nginx loads">
        {config.files.map((file, index) => {
          const open = file.target ?? file.path
          const lines = fileLines(file.content).length
          return (
            <ChoiceRow
              key={file.path}
              onSelect={() => onOpen(open)}
              verb={`Open ${relativePath(open, root)}`}
              className="min-h-0 gap-3 px-3 py-2.5"
              leading={
                <span className="numeric w-6 text-right text-hint text-muted-foreground">
                  {index + 1}
                </span>
              }
              title={<span className="font-mono">{relativePath(open, root)}</span>}
              description={
                file.target
                  ? `${plural(lines, "line")} · via ${relativePath(file.path, root)}`
                  : plural(lines, "line")
              }
            />
          )
        })}
      </ChoiceList>
    </div>
  )
}

/** One match: its line with the match marked, where it is, and the blocks it sits in. */
function MatchRow({
  match,
  root,
  onOpen,
}: {
  match: SearchMatch
  root: string
  onOpen: (path: string, line: number) => void
}) {
  const where = `${relativePath(match.open, root)}:${match.line}`
  return (
    <ChoiceRow
      onSelect={() => onOpen(match.open, match.line)}
      verb={`Open ${relativePath(match.open, root)} at line ${match.line}`}
      className="min-h-0 gap-3 px-3 py-2.5"
      title={
        <span className="font-mono text-body font-normal">
          {match.text.slice(0, match.start)}
          <mark className="rounded-sm bg-mark px-px text-foreground">
            {match.text.slice(match.start, match.end)}
          </mark>
          {match.text.slice(match.end)}
        </span>
      }
      description={
        <>
          <span className="font-mono">{where}</span>
          {match.within && match.within.length > 0 && ` · ${match.within.join(" › ")}`}
        </>
      }
    />
  )
}

/** "read 12s ago", kept current. */
function ReadAt({ at }: { at: number }) {
  const now = useNow(5000)
  return (
    <span className="numeric text-hint text-muted-foreground">
      {readLabel(at, Math.max(now, at))}
    </span>
  )
}
