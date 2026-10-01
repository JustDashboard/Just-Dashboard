"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import {
  ChevronDown,
  ChevronRight,
  Clock,
  Cross,
  Filter,
  Key,
  Layers,
  Plus,
  RefreshClockwise,
  SidebarLeftClose,
} from "@/components/icons"
import { bytes } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { EmptyState, Spinner } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { redisKeys, redisTree } from "@/components/database/redis/api"
import {
  bytesId,
  bytesLabel,
  globEscape,
  hasGlob,
  sameBytes,
} from "@/components/database/redis/bytes"
import { DbPicker } from "@/components/database/redis/db-picker"
import { KIND_ORDER, KindMark, REDIS_KINDS, kindOf } from "@/components/database/redis/kinds"
import {
  leafName,
  mergeKeys,
  mergeLevel,
  scanProgress,
  type KeyList,
  type TreeLevel,
} from "@/components/database/redis/keys/scan"
import { ReadError } from "@/components/database/redis/read-error"
import { ttlWord } from "@/components/database/redis/ttl"
import type { RedisBytes, RedisKey, RedisTreeFolder } from "@/components/database/redis/types"
import { usePaged, type Paged } from "@/components/database/redis/use-paged"
import type { Redis } from "@/components/database/redis/use-redis"
import { useRovingRows } from "@/components/database/redis/use-roving-rows"
import { useRowWindow } from "@/components/database/redis/use-row-window"

const ROW_HEIGHT = 28
/** How many namespaces of one level are drawn before the rest are asked for. */
const FOLDER_PAGE = 200

type RailView = "tree" | "list"

/** What every row of the rail needs to know about the page around it. */
type RailContext = {
  redis: Redis
  pattern: string
  type: string
  epoch: number
  selected: RedisBytes | undefined
  open: ReadonlySet<string>
  toggle: (prefix: RedisBytes) => void
  onOpen: (key: RedisBytes) => void
  /** Narrow the rail to one namespace, as a flat list. */
  onNarrow: (pattern: string) => void
}

/**
 * The keys of one logical database: which one, narrowed by a pattern and a
 * type, as a tree of namespaces or a flat list.
 *
 * Nothing here asks the server for the keyspace whole. The tree is the
 * server's own walk grouped by `:` — a level is read when it is opened, and a
 * walk that stopped at its budget says how far it got and offers the rest.
 * The list follows the scan's cursor a page at a time. Both say what they
 * have seen against how many keys there are, because "no more rows" and
 * "not scanned yet" are different facts.
 */
export function KeyRail({
  redis,
  selected,
  epoch,
  onOpen,
  onRefresh,
  onNew,
  onBulk,
  onHide,
}: {
  redis: Redis
  selected: RedisBytes | undefined
  /** Raised to read the keys again: Refresh, and after a key is made, renamed or removed. */
  epoch: number
  onOpen: (key: RedisBytes) => void
  onRefresh: () => void
  onNew: () => void
  /** Open the bulk actions on the pattern and type the rail is narrowed to. */
  onBulk: () => void
  onHide: () => void
}) {
  const { id, target, db, server, select, param, canWrite } = redis
  const pattern = param("pattern")
  const type = param("type")
  const [arranged, setView] = useViewState<RailView>(`databases.${id}.redis.view`, "tree")
  // An engine with no namespace walk has only the list.
  const tree = redis.engine.can("keyTree")
  const view: RailView = tree ? arranged : "list"
  const [opened, setOpened] = useSessionState<string[]>(
    `databases.${id}.redis.open.${db ?? "default"}`,
    [],
  )
  const open = useMemo(() => new Set(opened), [opened])
  const toggle = (prefix: RedisBytes) => {
    const key = bytesId(prefix)
    setOpened((held) => (held.includes(key) ? held.filter((p) => p !== key) : [...held, key]))
  }

  const scope = JSON.stringify([id, target.db ?? null, pattern, type])
  const root = usePaged(
    {
      fetch: (cursor, signal) => redisTree(target, { prefix: "", pattern, type, cursor }, signal),
      merge: mergeLevel,
      next: (page) => (page.complete ? null : page.cursor),
    },
    `tree:${scope}`,
    { epoch, enabled: view === "tree" },
  )
  const list = usePaged(
    {
      // The cursor goes back as it came. It is the server's own unsigned
      // 64-bit number, and one read into a JavaScript number comes back as
      // another cursor.
      fetch: (cursor, signal) =>
        redisKeys(target, { pattern, type, cursor: cursor ?? "0" }, signal),
      merge: mergeKeys,
      next: (page) => (page.done ? null : page.cursor),
    },
    `list:${scope}`,
    { epoch, enabled: view === "list" },
  )

  // The mix of types in the database, as the last unfiltered walk counted it:
  // a chip keeps its count while another one is pressed.
  const [mix, setMix] = useState<{ scope: string; types: Record<string, number> }>()
  const mixScope = JSON.stringify([id, target.db ?? null, pattern])
  if (view === "tree" && !type && root.state && mix?.types !== root.state.types) {
    setMix({ scope: mixScope, types: root.state.types })
  }
  const counts = mix?.scope === mixScope ? mix.types : undefined

  const features = server.data?.features
  // JSON where the product has it and this server has it loaded.
  const kinds = KIND_ORDER.filter(
    (kind) => kind !== "json" || (redis.engine.can("json") && features?.json),
  )

  const context: RailContext = {
    redis,
    pattern,
    type,
    epoch,
    selected,
    open,
    toggle,
    onOpen,
    onNarrow: (next) => {
      select({ pattern: next })
      setView("list")
    },
  }

  const narrowed = Boolean(pattern && pattern !== "*") || Boolean(type)
  const verbs: Verb[] = [
    ...(canWrite && redis.engine.can("bulkKeys")
      ? [{ key: "bulk", label: "Bulk actions…", icon: Layers, run: onBulk }]
      : []),
    ...(view === "tree" && opened.length > 0
      ? [{ key: "collapse", label: "Collapse all", icon: ChevronRight, run: () => setOpened([]) }]
      : []),
    { key: "hide", label: "Hide the keys", icon: SidebarLeftClose, run: onHide },
  ]

  return (
    <Pane flush data-slot="redis-key-rail" className="min-w-0 flex-1">
      <PaneHeader className="gap-1">
        {server.data ? (
          <DbPicker server={server.data} db={db} onChange={redis.setDb} />
        ) : (
          !server.error && <Skeleton className="h-7 w-20" />
        )}
        <span className="min-w-0 flex-1" />
        <IconAction
          label="Refresh the keys"
          className="size-7"
          onClick={onRefresh}
          disabled={root.reloading || list.reloading}
        >
          <RefreshClockwise />
        </IconAction>
        <VerbMenu verbs={verbs} label="More key actions" />
        {canWrite && (
          <Button size="xs" className="h-7" onClick={onNew}>
            <Plus />
            New key
          </Button>
        )}
      </PaneHeader>

      <div className="flex shrink-0 flex-col gap-2 border-b border-hairline p-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <PatternBox pattern={pattern} onApply={(next) => select({ pattern: next })} />
          {tree && (
            <div
              role="group"
              aria-label="Arrangement"
              className="flex shrink-0 items-center gap-0.5"
            >
              <FilterChip selected={view === "tree"} onClick={() => setView("tree")}>
                Tree
              </FilterChip>
              <FilterChip selected={view === "list"} onClick={() => setView("list")}>
                List
              </FilterChip>
            </div>
          )}
        </div>
        <ChipStrip aria-label="Key type" role="group" className="max-sm:mx-0 max-sm:px-0">
          {(redis.engine.can("keyTypeFilter") ? kinds : []).map((kind) => {
            const info = REDIS_KINDS[kind]
            const on = type === info.wire
            const count = counts?.[info.wire]
            return (
              <FilterChip
                key={kind}
                selected={on}
                onClick={() => select({ type: on ? null : info.wire })}
                className="gap-1 px-2"
              >
                <KindMark type={info.wire} className="size-3" />
                {info.label}
                {count !== undefined && <ChipCount>{count.toLocaleString()}</ChipCount>}
              </FilterChip>
            )
          })}
        </ChipStrip>
      </div>

      {view === "tree" ? (
        <TreeBody
          context={context}
          root={root}
          narrowed={narrowed}
          onNew={canWrite ? onNew : undefined}
        />
      ) : (
        <ListBody
          context={context}
          list={list}
          narrowed={narrowed}
          onNew={canWrite ? onNew : undefined}
        />
      )}

      <RailFoot view={view} root={root} list={list} narrowed={narrowed} />
    </Pane>
  )
}

/**
 * The pattern, typed. It is applied on Enter and when the reader pauses: each
 * pattern is a scan of the keyspace, and one per keystroke would be a scan
 * per letter of a name.
 */
function PatternBox({ pattern, onApply }: { pattern: string; onApply: (next: string) => void }) {
  const [draft, setDraft] = useState(pattern)
  // The address can change the pattern from under the box — a namespace
  // narrowed to, Back — and the box then says what the address says. What
  // the reader is in the middle of typing is not replaced by its own echo.
  const [applied, setApplied] = useState(pattern)
  if (applied !== pattern) {
    setApplied(pattern)
    if (draft.trim() !== pattern) setDraft(pattern)
  }
  const apply = useRef(onApply)
  useEffect(() => {
    apply.current = onApply
  })
  useEffect(() => {
    if (draft.trim() === pattern) return
    const timer = setTimeout(() => apply.current(draft.trim()), 450)
    return () => clearTimeout(timer)
  }, [draft, pattern])
  return (
    <SearchInput
      dense
      aria-label="Key pattern"
      placeholder="Pattern — user:*"
      value={draft}
      spellCheck={false}
      autoComplete="off"
      className="pr-7 font-mono"
      containerClassName="flex-1 sm:w-auto"
      onChange={(event) => setDraft(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === "Enter") onApply(draft.trim())
        if (event.key === "Escape" && draft) setDraft("")
      }}
      trailing={
        draft && (
          <button
            type="button"
            aria-label="Clear the pattern"
            onClick={() => {
              setDraft("")
              onApply("")
            }}
            className="flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            <Cross className="size-3" />
          </button>
        )
      }
    />
  )
}

function RailSkeleton() {
  return (
    <div className="space-y-1.5 p-2" aria-hidden>
      {Array.from({ length: 12 }, (_, i) => (
        <div key={i} className="flex h-5 items-center gap-2">
          <Skeleton className="size-3.5 shrink-0" />
          <Skeleton className="h-3" style={{ width: `${38 + ((i * 17) % 44)}%` }} />
          <Skeleton className="ml-auto h-3 w-8" />
        </div>
      ))}
    </div>
  )
}

/** Nothing matched, or nothing is there: which of the two, and what to do about it. */
function NoKeys({
  context,
  narrowed,
  onNew,
}: {
  context: RailContext
  narrowed: boolean
  onNew?: () => void
}) {
  const { pattern, type, redis } = context
  const named = pattern && !hasGlob(pattern)
  if (!narrowed) {
    return (
      <EmptyState
        icon={Key}
        className="border-0"
        title={`db${redis.db ?? ""} holds no keys`}
        description="Create the first one here, or pick another database above."
        action={
          onNew && (
            <Button size="sm" onClick={onNew}>
              <Plus />
              New key
            </Button>
          )
        }
      />
    )
  }
  return (
    <EmptyState
      icon={Filter}
      className="border-0"
      title={named ? "No key has that name" : "No key matches"}
      description={
        named
          ? "A pattern with no * or ? is one key's exact name."
          : `Nothing in db${redis.db ?? ""} matches ${pattern || "*"}${type ? ` as ${kindOf(type).label.toLowerCase()}` : ""}.`
      }
      action={
        <div className="flex flex-wrap justify-center gap-2">
          {named && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => redis.select({ pattern: `*${globEscape(pattern)}*` })}
            >
              Search names containing it
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={() => redis.select({ pattern: null, type: null })}
          >
            Clear the filter
          </Button>
        </div>
      }
    />
  )
}

function TreeBody({
  context,
  root,
  narrowed,
  onNew,
}: {
  context: RailContext
  root: Paged<TreeLevel>
  narrowed: boolean
  onNew?: () => void
}) {
  const { attach, onKeyDown, onFocus } = useRovingRows<HTMLDivElement>()
  const level = root.state
  return (
    <div
      ref={attach}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      role="group"
      aria-label="Keys by namespace"
      className="min-h-0 flex-1 overflow-auto py-1"
    >
      {root.error && !level ? (
        <ReadError error={root.error} onRetry={root.reload} className="m-2" />
      ) : !level ? (
        <RailSkeleton />
      ) : level.folders.length === 0 && level.keys.length === 0 && root.done ? (
        <NoKeys context={context} narrowed={narrowed} onNew={onNew} />
      ) : (
        <LevelRows context={context} level={level} prefix="" depth={0} />
      )}
    </div>
  )
}

/** The namespaces and the keys directly under one prefix. */
function LevelRows({
  context,
  level,
  prefix,
  depth,
  more,
}: {
  context: RailContext
  level: TreeLevel
  prefix: RedisBytes
  depth: number
  /** For a level below the top: its own walk stopped short, and this continues it. */
  more?: { run: () => void; busy: boolean }
}) {
  const [shown, setShown] = useState(FOLDER_PAGE)
  const hidden = level.keyCount - level.keys.length
  return (
    <>
      {level.folders.slice(0, shown).map((folder) => (
        <FolderRow key={bytesId(folder.prefix)} context={context} folder={folder} depth={depth} />
      ))}
      {level.folders.length > shown && (
        <NoteRow depth={depth} onClick={() => setShown(shown + FOLDER_PAGE)}>
          Show {Math.min(FOLDER_PAGE, level.folders.length - shown).toLocaleString()} more
          namespaces of {level.folders.length.toLocaleString()}
        </NoteRow>
      )}
      {level.foldersOmitted > 0 && (
        <NoteRow depth={depth}>
          {level.foldersOmitted.toLocaleString()} more namespaces — narrow the pattern to see them
        </NoteRow>
      )}
      {level.keys.map((entry) => (
        <KeyRow
          key={bytesId(entry.key)}
          entry={entry}
          name={leafName(entry.key, prefix)}
          depth={depth}
          tree
          current={sameBytes(entry.key, context.selected)}
          onOpen={context.onOpen}
        />
      ))}
      {hidden > 0 && typeof prefix === "string" && (
        <NoteRow depth={depth} onClick={() => context.onNarrow(`${globEscape(prefix)}*`)}>
          {hidden.toLocaleString()} more keys here — list them all
        </NoteRow>
      )}
      {more && (
        <NoteRow depth={depth} onClick={more.busy ? undefined : more.run}>
          {more.busy ? "Scanning…" : "Not every key was walked — scan more"}
        </NoteRow>
      )}
    </>
  )
}

const indent = (depth: number) => ({ paddingLeft: `${depth * 14 + 8}px` })

const ROW =
  "flex h-7 w-full min-w-0 items-center gap-1.5 pr-2 text-left text-xs transition-colors focus-ring-inset hover:bg-row-hover"

function FolderRow({
  context,
  folder,
  depth,
}: {
  context: RailContext
  folder: RedisTreeFolder
  depth: number
}) {
  const { redis, pattern, type, epoch, open, toggle, onNarrow } = context
  const id = bytesId(folder.prefix)
  const isOpen = open.has(id)
  const level = usePaged(
    {
      fetch: (cursor, signal) =>
        redisTree(redis.target, { prefix: folder.prefix, pattern, type, cursor }, signal),
      merge: mergeLevel,
      next: (page) => (page.complete ? null : page.cursor),
    },
    `tree:${JSON.stringify([redis.id, redis.target.db ?? null, pattern, type, id])}`,
    { epoch, enabled: isOpen },
  )
  const name = bytesLabel(folder.name)
  const Chevron = isOpen ? ChevronDown : ChevronRight
  return (
    <>
      <div className="group flex min-w-0 items-center">
        <button
          type="button"
          data-roving
          tabIndex={-1}
          aria-expanded={isOpen}
          title={bytesLabel(folder.prefix)}
          onClick={() => toggle(folder.prefix)}
          style={indent(depth)}
          className={cn(ROW, "flex-1")}
        >
          <Chevron aria-hidden className="size-3 shrink-0 text-muted-foreground" />
          <span className="min-w-0 truncate font-mono">{name || "(empty)"}</span>
          <span className="numeric ml-auto shrink-0 pl-2 text-hint text-muted-foreground">
            {folder.count.toLocaleString()}
          </span>
        </button>
        {typeof folder.pattern === "string" ? (
          <IconAction
            reveal
            label={`List the keys of ${name}`}
            className="mr-1 size-6"
            onClick={() => onNarrow(folder.pattern as string)}
          >
            <Filter />
          </IconAction>
        ) : (
          // The control's column, kept: every count of the tree ends on one edge.
          <span className="mr-1 size-6 shrink-0" />
        )}
      </div>
      {isOpen &&
        (level.error && !level.state ? (
          <NoteRow depth={depth + 1} tone="danger" onClick={level.reload}>
            {level.error.message} — try again
          </NoteRow>
        ) : !level.state ? (
          <div style={indent(depth + 1)} className="flex h-7 items-center gap-2 pr-2">
            <Skeleton className="h-3 w-32" />
          </div>
        ) : (
          <LevelRows
            context={context}
            level={level.state}
            prefix={folder.prefix}
            depth={depth + 1}
            more={level.done ? undefined : { run: level.more, busy: level.loadingMore }}
          />
        ))}
    </>
  )
}

/** One key: its type's mark, its name, and how large it is. */
function KeyRow({
  entry,
  name,
  depth,
  tree,
  current,
  onOpen,
}: {
  entry: RedisKey
  name: string
  depth: number
  /** A row of the tree: its size ends where a namespace's count does, short of the row's control. */
  tree?: boolean
  current: boolean
  onOpen: (key: RedisBytes) => void
}) {
  const label = bytesLabel(entry.key)
  const size = entry.type === "string" ? bytes(entry.size, 0) : entry.size.toLocaleString()
  return (
    <button
      type="button"
      data-roving
      tabIndex={-1}
      aria-current={current ? "true" : undefined}
      aria-label={`Open ${label}`}
      title={entry.ttl >= 0 ? `${label} — expires in ${ttlWord(entry.ttl)}` : label}
      onClick={() => onOpen(entry.key)}
      style={indent(depth)}
      className={cn(ROW, tree && "pr-9", current && "bg-accent hover:bg-accent")}
    >
      {/* The chevron's width, so a key's mark sits under its namespace's name. */}
      {depth > 0 && <span className="w-3 shrink-0" />}
      <KindMark type={entry.type} />
      <span className="min-w-0 truncate font-mono">{name || "(empty name)"}</span>
      <span className="ml-auto flex shrink-0 items-center gap-1 pl-2 text-hint text-muted-foreground">
        {entry.ttl >= 0 && <Clock aria-hidden className="size-3 opacity-70" />}
        {kindOf(entry.type).member && <span className="numeric">{size}</span>}
      </span>
    </button>
  )
}

/** A line of the tree that is about the tree: how much more there is, and the press that fetches it. */
function NoteRow({
  depth,
  tone,
  onClick,
  children,
}: {
  depth: number
  tone?: "danger"
  onClick?: () => void
  children: React.ReactNode
}) {
  const text = cn(
    "min-w-0 truncate text-hint",
    tone === "danger" ? "text-destructive" : "text-muted-foreground",
  )
  if (!onClick) {
    return (
      <div style={indent(depth)} className="flex h-7 items-center pr-2">
        <span className={cn(text, "pl-4.5")}>{children}</span>
      </div>
    )
  }
  return (
    <button
      type="button"
      data-roving
      tabIndex={-1}
      onClick={onClick}
      style={indent(depth)}
      className={cn(ROW, "hover:text-foreground")}
    >
      <span className={cn(text, "pl-4.5")}>{children}</span>
    </button>
  )
}

function ListBody({
  context,
  list,
  narrowed,
  onNew,
}: {
  context: RailContext
  list: Paged<KeyList>
  narrowed: boolean
  onNew?: () => void
}) {
  const { attach, onKeyDown, onFocus } = useRovingRows<HTMLDivElement>()
  const keys = list.state?.keys ?? []
  const {
    attach: scroller,
    onScroll,
    slice,
    height,
    offset,
  } = useRowWindow(keys.length, ROW_HEIGHT)
  return (
    <div
      ref={attach}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      role="group"
      aria-label="Keys"
      className="flex min-h-0 flex-1 flex-col"
    >
      <div ref={scroller} onScroll={onScroll} className="min-h-0 flex-1 overflow-auto">
        {list.error && !list.state ? (
          <ReadError error={list.error} onRetry={list.reload} className="m-2" />
        ) : !list.state ? (
          <RailSkeleton />
        ) : keys.length === 0 && list.done ? (
          <NoKeys context={context} narrowed={narrowed} onNew={onNew} />
        ) : (
          <div style={{ height }} className="relative">
            <div style={{ transform: `translateY(${offset}px)` }}>
              {keys.slice(slice.start, slice.end).map((entry) => (
                <KeyRow
                  key={bytesId(entry.key)}
                  entry={entry}
                  name={bytesLabel(entry.key)}
                  depth={0}
                  current={sameBytes(entry.key, context.selected)}
                  onOpen={context.onOpen}
                />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

/** How far the scan has come, and the press that takes it further. */
function RailFoot({
  view,
  root,
  list,
  narrowed,
}: {
  view: RailView
  root: Paged<TreeLevel>
  list: Paged<KeyList>
  narrowed: boolean
}) {
  const scan = view === "tree" ? root : list
  if (!scan.state) return null
  const words =
    view === "tree" && root.state
      ? scanProgress({
          found: root.state.count,
          scanned: root.state.scanned,
          total: root.state.total,
          complete: root.done,
          filtered: narrowed,
        })
      : list.state
        ? scanProgress({
            found: list.state.keys.length,
            total: list.state.total,
            complete: list.done,
            filtered: narrowed,
          })
        : ""
  return (
    <PaneFooter className="gap-2 text-hint text-muted-foreground">
      <span data-slot="redis-scan-progress" className="numeric min-w-0 flex-1 truncate">
        {scan.error ? `${words} — the last read failed` : words}
      </span>
      {scan.reloading && <Spinner className="size-3" />}
      {!scan.done && (
        <Button size="xs" variant="outline" pending={scan.loadingMore} onClick={scan.more}>
          Scan more
        </Button>
      )}
    </PaneFooter>
  )
}
