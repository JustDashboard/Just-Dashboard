"use client"

import { useState } from "react"
import { ChevronDown, ChevronRight, SidebarRightClose } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList, SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { EmptyNote, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ClassTag } from "@/components/database/redis/console/console-view"
import { groupCommands } from "@/components/database/redis/console/complete"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisCommandRef, RedisCommandReference } from "@/components/database/redis/types"

/**
 * The server's commands, by group, with what each one does.
 *
 * It is the server's own reference — its `COMMAND` and, where it has them,
 * its `COMMAND DOCS` — so it lists what this product and its loaded modules
 * answer to and nothing else. A server that does not describe its commands
 * (KeyDB, Dragonfly, Redis before 7) still lists them, with how many
 * arguments each takes and what the dashboard classes it as.
 */
export function CommandHelper({
  reference,
  error,
  onRetry,
  onInsert,
  onClose,
}: {
  reference: RedisCommandReference | undefined
  error: Error | undefined
  onRetry: () => void
  /** Put a command's name in the prompt. */
  onInsert: (command: RedisCommandRef) => void
  onClose: () => void
}) {
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState<string[]>([])
  const [shown, setShown] = useState("")
  const groups = reference ? groupCommands(reference.commands, query) : []
  const searching = query.trim() !== ""

  return (
    <Pane flush data-slot="redis-command-helper" className="min-w-0 flex-1">
      <PaneHeader className="gap-1.5">
        <span className="min-w-0 flex-1 truncate text-xs font-medium">Commands</span>
        {reference && (
          <span className="numeric text-hint text-muted-foreground">
            {reference.commands.length.toLocaleString()}
          </span>
        )}
        <IconAction label="Hide the commands" className="size-7" onClick={onClose}>
          <SidebarRightClose />
        </IconAction>
      </PaneHeader>
      <div className="shrink-0 border-b border-hairline p-2">
        <SearchInput
          dense
          aria-label="Find a command"
          placeholder="Find a command"
          value={query}
          spellCheck={false}
          autoComplete="off"
          containerClassName="sm:w-full"
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-auto py-1">
        {error && !reference ? (
          <ReadError error={error} onRetry={onRetry} className="m-2" />
        ) : !reference ? (
          <LoadingRows rows={8} className="p-2" />
        ) : groups.length === 0 ? (
          <EmptyNote>No command matches {query}.</EmptyNote>
        ) : (
          groups.map(({ group, commands }) => {
            const expanded = searching || open.includes(group)
            const Chevron = expanded ? ChevronDown : ChevronRight
            return (
              <div key={group}>
                <button
                  type="button"
                  aria-expanded={expanded}
                  disabled={searching}
                  onClick={() =>
                    setOpen((held) =>
                      held.includes(group) ? held.filter((g) => g !== group) : [...held, group],
                    )
                  }
                  className="flex h-7 w-full items-center gap-1.5 px-2 text-left text-xs focus-ring-inset transition-colors hover:bg-row-hover disabled:hover:bg-transparent"
                >
                  <Chevron aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate font-medium">{group}</span>
                  <span className="numeric text-hint text-muted-foreground">{commands.length}</span>
                </button>
                {expanded &&
                  commands.map((command) => (
                    <CommandRow
                      key={command.name}
                      command={command}
                      documented={reference.documented}
                      open={shown === command.name}
                      onToggle={() => setShown(shown === command.name ? "" : command.name)}
                      onInsert={() => onInsert(command)}
                    />
                  ))}
              </div>
            )
          })
        )}
      </div>
      {reference && !reference.documented && (
        <p className="shrink-0 border-t border-hairline px-2.5 py-2 text-hint text-pretty text-muted-foreground">
          This server lists its commands without describing them, so there is no summary or syntax
          to show.
        </p>
      )}
    </Pane>
  )
}

function CommandRow({
  command,
  documented,
  open,
  onToggle,
  onInsert,
}: {
  command: RedisCommandRef
  documented: boolean
  open: boolean
  onToggle: () => void
  onInsert: () => void
}) {
  return (
    <div className={cn(open && "bg-surface-header/50")}>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className="flex h-7 w-full min-w-0 items-center gap-2 pr-2 pl-7 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{command.name}</span>
        <span className="flex shrink-0 items-center gap-1.5">
          {command.deprecated && <Tag>deprecated</Tag>}
          <ClassTag value={command.class} admin={command.admin} />
        </span>
      </button>
      {open && (
        <div className="space-y-2 pt-1 pr-2.5 pb-3 pl-7">
          {command.summary && (
            <p className="text-xs leading-relaxed text-pretty">{command.summary}</p>
          )}
          {command.syntax && (
            <p className="rounded-sm bg-surface-sunken px-2 py-1.5 font-mono text-hint leading-relaxed break-words">
              <span className="text-foreground">{command.name}</span>{" "}
              <span className="text-muted-foreground">{command.syntax}</span>
            </p>
          )}
          <DetailList>
            {command.since && <Detail label="Since">{command.since}</Detail>}
            {command.complexity && <Detail label="Cost">{command.complexity}</Detail>}
            {!documented && (
              <Detail label="Arguments">
                {command.arity < 0
                  ? `at least ${Math.abs(command.arity) - 1}`
                  : String(command.arity - 1)}
              </Detail>
            )}
            {command.flags.length > 0 && (
              <Detail label="Flags" className="font-mono text-hint">
                {command.flags.join(" ")}
              </Detail>
            )}
          </DetailList>
          <Button size="xs" variant="outline" onClick={onInsert}>
            Put it in the prompt
          </Button>
        </div>
      )}
    </div>
  )
}
