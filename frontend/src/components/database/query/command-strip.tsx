"use client"

import { ChevronDown, FloppyDisk, Play, Route, StopCircle, TextFormat } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { Standing } from "@/components/database/query/editor"
import { riskWord, type Gate } from "@/components/database/query/gate"
import { useKeyNames } from "@/components/database/query/keys"
import { ROW_LIMITS, useElapsed } from "@/components/database/query/results"
import { elapsedText, type Run } from "@/components/database/query/run-model"
import type { Risk } from "@/components/database/query/types"

const LIMITS = ROW_LIMITS.map((limit) => ({
  value: String(limit),
  label: limit.toLocaleString("en-US"),
}))

/** What Run would send, in a few words: "Selection", "Statement 2 of 5". */
function standingWords(standing: Standing): string {
  if (!standing.target) return ""
  if (standing.target.scope === "selection") return "Selection"
  if (standing.of > 1) return `Statement ${standing.at} of ${standing.of}`
  return ""
}

/**
 * The editor's commands, between the statement and what it returns.
 *
 * Run is the one brand-faced command of the page. While a statement is out it
 * gives its place to Cancel and a clock, so the control under the reader's
 * hand is always the one that applies. Beside it the strip says what Run
 * would send and what the server makes of it — and, for a statement the role
 * or the connection will not take, why not, before the press rather than
 * after it.
 */
export function CommandStrip({
  compact,
  standing,
  advice,
  verdict,
  run,
  checking,
  canRun,
  canAnalyze,
  canCancel,
  canTransact,
  canSave,
  saved,
  changed,
  transaction,
  onTransaction,
  limit,
  onLimit,
  onRun,
  onCancel,
  onExplain,
  onFormat,
  onSave,
  empty,
}: {
  /** The strip is too narrow for words on every control. */
  compact: boolean
  standing: Standing
  /** The server's reading of what Run would send, when it is of exactly that text. */
  advice: Risk | undefined
  /** What that reading means for this reader. */
  verdict: Gate | undefined
  /** This tab's run, while it is out. */
  run: Run | undefined
  checking: boolean
  canRun: boolean
  canAnalyze: boolean
  canCancel: boolean
  canTransact: boolean
  canSave: boolean
  /** The tab is the draft of a saved query. */
  saved: boolean
  changed: boolean
  transaction: boolean
  onTransaction: (on: boolean) => void
  limit: number
  onLimit: (limit: number) => void
  onRun: (all: boolean) => void
  onCancel: () => void
  onExplain: (analyze: boolean) => void
  onFormat: () => void
  onSave: () => void
  /** The editor holds nothing to run. */
  empty: boolean
}) {
  const keys = useKeyNames()
  const running = run?.phase === "running"
  const refused = verdict && !verdict.run ? verdict.why : undefined
  const words = standingWords(standing)
  return (
    <div
      data-slot="query-commands"
      className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-y border-hairline bg-surface-header px-2 py-1"
    >
      {canRun &&
        (running ? (
          <RunningControls run={run} canCancel={canCancel} onCancel={onCancel} />
        ) : (
          <div className="flex shrink-0 items-center">
            <Button
              size="sm"
              className="h-7 rounded-r-none px-2.5 max-sm:h-8"
              disabled={empty || refused !== undefined}
              pending={checking}
              title={keys.run}
              onClick={() => onRun(false)}
            >
              <Play />
              Run
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="sm"
                  aria-label="More ways to run"
                  className="h-7 rounded-l-none border-l border-brand-foreground/20 px-1 max-sm:h-8"
                  // What refuses the statement refuses the text it is part of.
                  disabled={empty || refused !== undefined}
                >
                  <ChevronDown />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-72">
                <DropdownMenuItem onSelect={() => onRun(false)}>
                  <span className="flex-1">Run the selection or statement</span>
                  <span className="text-hint text-muted-foreground">{keys.run}</span>
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onRun(true)}>
                  <span className="flex-1">Run everything</span>
                  <span className="text-hint text-muted-foreground">{keys.runAll}</span>
                </DropdownMenuItem>
                {canTransact && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuCheckboxItem
                      checked={transaction}
                      onSelect={(event) => event.preventDefault()}
                      onCheckedChange={(checked) => onTransaction(checked === true)}
                    >
                      Several statements as one transaction
                    </DropdownMenuCheckboxItem>
                  </>
                )}
                {compact && (
                  <>
                    <DropdownMenuSeparator />
                    <p className="px-2 pt-1.5 pb-1 text-hint text-muted-foreground">
                      Rows per statement
                    </p>
                    <DropdownMenuRadioGroup
                      value={String(limit)}
                      onValueChange={(value) => onLimit(Number(value))}
                    >
                      {LIMITS.map((option) => (
                        <DropdownMenuRadioItem key={option.value} value={option.value}>
                          {option.label}
                        </DropdownMenuRadioItem>
                      ))}
                    </DropdownMenuRadioGroup>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ))}

      <div className="flex shrink-0 items-center">
        <Button
          size="xs"
          variant="outline"
          aria-label="Explain"
          className={cn("h-7 max-sm:h-8", canAnalyze && "rounded-r-none")}
          disabled={empty}
          onClick={() => onExplain(false)}
        >
          <Route />
          Explain
        </Button>
        {canAnalyze && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="xs"
                variant="outline"
                aria-label="More ways to explain"
                className="h-7 rounded-l-none border-l-0 px-1 max-sm:h-8"
                disabled={empty}
              >
                <ChevronDown />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-72">
              <DropdownMenuItem onSelect={() => onExplain(false)}>
                <span className="flex-1">Explain</span>
                <span className="text-hint text-muted-foreground">runs nothing</span>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onExplain(true)}>
                <span className="flex-1">Explain and measure</span>
                <span className="text-hint text-muted-foreground">runs the statement</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {canRun && !compact && (
        <Segments
          label="Rows per statement"
          value={String(limit)}
          options={LIMITS}
          onChange={(value) => onLimit(Number(value))}
          className="shrink-0"
        />
      )}

      <Button
        size="xs"
        variant="ghost"
        aria-label="Format"
        className="h-7 shrink-0 max-sm:h-8"
        title={keys.format}
        disabled={empty}
        onClick={onFormat}
      >
        <TextFormat />
        {!compact && "Format"}
      </Button>
      {canSave && (
        <Button
          size="xs"
          variant="ghost"
          aria-label={saved ? "Save" : "Save as"}
          className="h-7 shrink-0 max-sm:h-8"
          title={keys.save}
          disabled={empty || (saved && !changed)}
          onClick={onSave}
        >
          <FloppyDisk />
          {!compact && (saved ? "Save" : "Save as…")}
        </Button>
      )}

      <div
        aria-live="polite"
        className={cn(
          "ml-auto flex min-w-0 items-center justify-end gap-2 text-hint text-muted-foreground",
          // A sentence takes what is left of the line and is cut there, rather
          // than dropping to a line of its own under the commands.
          (!canRun || refused) && "flex-1 basis-40",
        )}
      >
        {!canRun && (
          <span className="min-w-0 truncate">
            Your role reads this database and may not run statements.
          </span>
        )}
        {canRun && refused && (
          <span data-slot="run-refused" className="min-w-0 truncate text-warning" title={refused}>
            {refused}
          </span>
        )}
        {canRun && !refused && words && <span className="shrink-0 max-sm:hidden">{words}</span>}
        {canRun && !refused && advice && (
          <Tag
            tone={
              advice.level === "critical" ? "danger" : advice.destructive ? "warning" : "default"
            }
            title={advice.reasons.join("; ") || "It only reads"}
          >
            {riskWord(advice)}
          </Tag>
        )}
      </div>
    </div>
  )
}

/** Cancel in Run's place while the statement is out, with how long it has been. */
function RunningControls({
  run,
  canCancel,
  onCancel,
}: {
  run: Run
  canCancel: boolean
  onCancel: () => void
}) {
  const elapsed = useElapsed(run.startedAt)
  return (
    <div className="flex shrink-0 items-center gap-2">
      {canCancel && (
        <Button
          size="sm"
          variant="outline"
          className="h-7 px-2.5 max-sm:h-8"
          disabled={run.cancelling}
          onClick={onCancel}
        >
          <StopCircle />
          Cancel
        </Button>
      )}
      <span role="status" className="flex items-center gap-1.5 text-xs">
        <TextShimmer>
          {run.cancelling ? "Stopping…" : run.fetching ? "Fetching…" : "Running…"}
        </TextShimmer>
        <span className="numeric text-muted-foreground">{elapsedText(elapsed)}</span>
      </span>
    </div>
  )
}
