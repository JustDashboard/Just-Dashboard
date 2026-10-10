"use client"

import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { SidebarLeftClose, SidebarLeftOpen } from "@/components/icons"
import { del, errorMessage, post, put } from "@/lib/api"
import { usePanelSize } from "@/lib/panel-size"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Field, FormFact, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { ResizeHandle } from "@/components/resize-handle"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { CommandStrip } from "@/components/database/query/command-strip"
import type { SchemaTable } from "@/components/database/query/completion"
import { dialectOf, qualifiedName, quoteName } from "@/components/database/query/dialect"
import { SqlEditor, type SqlEditorHandle, type Standing } from "@/components/database/query/editor"
import { errorPlace } from "@/components/database/query/error-place"
import { gate } from "@/components/database/query/gate"
import { RESERVED, vocabularyOf } from "@/components/database/query/keywords"
import { forgetDocument, type TableColumn } from "@/components/database/query/monaco"
import { QueryRail, type RailView } from "@/components/database/query/rail"
import { Results, ROW_LIMITS, type ResultsViewId } from "@/components/database/query/results"
import { definesSchema, type RunStep } from "@/components/database/query/run-model"
import { snippetsFor } from "@/components/database/query/snippets"
import { SplitHandle } from "@/components/database/query/split-handle"
import { firstLine, splitStatements } from "@/components/database/query/sql-text"
import { TabStrip, tabElementId } from "@/components/database/query/tab-strip"
import {
  MAX_TABS,
  NO_TABS,
  activeTab,
  bindSaved,
  closeTab,
  followSaved,
  handOver,
  hasRoom,
  isChanged,
  newTab,
  openTab,
  renameTab,
  restoreTab,
  setTabSql,
  showTab,
  titleOf,
  withTab,
  type Opening,
  type TabsState,
} from "@/components/database/query/tabs"
import type { ClassifyResponse, SavedQuery } from "@/components/database/query/types"
import { useAsking } from "@/components/database/query/use-asking"
import {
  readColumns,
  useHistory,
  useSavedQueries,
  useSchema,
} from "@/components/database/query/use-query-data"
import { useResultExport } from "@/components/database/query/use-result-export"
import { useRunner } from "@/components/database/query/use-runner"

const RAIL = { fallback: 272, min: 208, max: 480 }
const SPLIT = { fallback: 0.58, min: 0.15, max: 0.8 }
const DEFAULT_LIMIT = 500
const NO_STANDING: Standing = { target: null, at: 0, of: 0 }
const RAIL_VIEWS: readonly RailView[] = ["saved", "history", "schema", "snippets"]

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

/**
 * The SQL editor: statements in tabs, run against this connection, with what
 * they returned under them and a rail of what has been kept, what has been
 * run, what the connection holds and the diagnostics written for its engine.
 *
 * A tab is a draft, kept per connection for the browser tab, and nothing ever
 * writes over one: a statement handed in by another page (`?sql=`), a history
 * entry, a snippet and a saved query each open in a tab of their own. The
 * hand-over is read once and taken out of the address, so a reload or Back
 * does not bring it in again. At the limit of tabs the reader is told, and a
 * hand-over waits in the address until a tab has been closed for it.
 *
 * What a statement would do is the server's to say, and it is asked every
 * time, of the exact text about to run (`use-runner`): nothing is run on the
 * strength of what was classified a moment ago.
 */
export function SqlQuery() {
  const { id, conn, engine, selection, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useAsking()
  const ids = useId()
  const panelId = `${ids}-editor`
  const dialect = useMemo(() => dialectOf(engine.driver), [engine.driver])
  const vocabulary = useMemo(() => vocabularyOf(engine.driver), [engine.driver])
  const snippets = useMemo(() => snippetsFor(engine.id, engine.driver), [engine.id, engine.driver])

  /* ---------------------------------------------------------------- tabs */

  const [stored, setStored] = useSessionState<TabsState>(`databases.${id}.query.tabs`, NO_TABS)
  const tabs = useMemo(() => withTab(stored), [stored])
  const setTabs = useCallback(
    (change: (state: TabsState) => TabsState) => setStored((held) => change(withTab(held))),
    [setStored],
  )
  const tab = activeTab(tabs)

  // Opens a statement where there is somewhere for it to go, and says so where
  // there is not: at the limit, with every tab holding something, an opening
  // used to do nothing at all.
  const place = useCallback(
    (opening: Opening, how: (state: TabsState) => TabsState): boolean => {
      if (!hasRoom(tabs, opening)) {
        notify.warning(`${MAX_TABS} tabs are open`, {
          description: "Close one to open this statement.",
        })
        return false
      }
      setTabs(how)
      return true
    },
    [tabs, setTabs],
  )

  // A statement handed over in the address opens in a tab of its own and is
  // taken out of the address. It is read from the router rather than from the
  // section's selection: the selection can still be answering with an earlier
  // write of this page's to the same address, and would hide the same
  // statement handed over a second time.
  const search = useSearchParams()
  const handed = search.get("sql") ?? ""
  const taken = useRef("")
  const waiting = useRef("")
  useEffect(() => {
    if (!handed) {
      taken.current = ""
      waiting.current = ""
      return
    }
    if (taken.current === handed) return
    // No tab for it: it stays in the address, said once, and is opened when
    // a tab has been closed.
    if (!hasRoom(tabs, { sql: handed })) {
      if (waiting.current !== handed) {
        waiting.current = handed
        notify.warning(`${MAX_TABS} tabs are open`, {
          description: "Close one, and the statement handed over opens in its place.",
          duration: 12_000,
        })
      }
      return
    }
    taken.current = handed
    waiting.current = ""
    setTabs((state) => handOver(state, handed))
    select({ sql: null })
  }, [handed, tabs, setTabs, select])

  const saved = useSavedQueries(id)
  const history = useHistory(id)

  // A link to a saved query (`?saved=<id>`) opens it in its tab, once the list
  // has been read, and is taken out of the address the same way.
  const linked = search.get("saved") ?? ""
  const followed = useRef("")
  useEffect(() => {
    if (!linked) {
      followed.current = ""
      return
    }
    if (followed.current === linked || !saved.data) return
    followed.current = linked
    const query = saved.data.find((entry) => String(entry.id) === linked)
    if (query) {
      const opening = { sql: query.sql, title: query.name, saved: query.id }
      place(opening, (state) => openTab(state, opening))
    } else notify.info("That saved query is not here any more")
    select({ saved: null })
  }, [linked, saved.data, place, select])

  /* -------------------------------------------------------------- layout */

  const [frame, frameWidth] = useColumnWidth<HTMLDivElement>()
  const [main, mainWidth] = useColumnWidth<HTMLDivElement>()
  const railBeside = frameWidth === 0 || frameWidth >= 720
  const [railPinned, setRailPinned] = useViewState("databases.query.rail", true)
  const [railOver, setRailOver] = useState(false)
  const railShown = railBeside ? railPinned : railOver
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize(
    "databases.query.rail",
    RAIL.fallback,
  )
  const railMax = Math.max(RAIL.min, Math.min(RAIL.max, frameWidth - 420))
  const railColumn = clamp(railWidth, RAIL.min, railMax)
  const [railStored, setRailView] = useViewState<RailView>("databases.query.rail.view", "schema")
  const railView = RAIL_VIEWS.includes(railStored) ? railStored : "schema"
  const [split, setSplit] = useViewState("databases.query.split", SPLIT.fallback)
  const share = clamp(typeof split === "number" ? split : SPLIT.fallback, SPLIT.min, SPLIT.max)
  const compact = mainWidth > 0 && mainWidth < 640

  /* -------------------------------------------------------------- schema */

  const schema = useSchema(id, selection.schema)
  // Named from the catalogue while the outline is still on its way, so the
  // picker never says "default" for a schema whose name is already known.
  const picked = selection.schema || schema.model.defaultSchema || schema.head?.defaultSchema || ""

  // A table's columns with their types, for the note under the pointer: read
  // once per table and kept while the page is.
  const described = useRef(new Map<string, Promise<TableColumn[]>>())
  const describe = useCallback(
    (ofSchema: string, table: string) => {
      const key = `${ofSchema}\u0000${table}`
      let read = described.current.get(key)
      if (!read) {
        read = readColumns(id, ofSchema, table)
        described.current.set(key, read)
        // One that failed is asked for again the next time.
        read.catch(() => described.current.delete(key))
      }
      return read
    },
    [id],
  )

  /* ----------------------------------------------------------- the editor */

  const editor = useRef<SqlEditorHandle>(null)
  const [standing, setStanding] = useState<Standing>(NO_STANDING)
  const onStanding = useCallback((next: Standing) => {
    setStanding((held) =>
      held.at === next.at &&
      held.of === next.of &&
      held.target?.sql === next.target?.sql &&
      held.target?.scope === next.target?.scope &&
      held.target?.offset === next.target?.offset
        ? held
        : next,
    )
  }, [])

  // What the server makes of the statement Run would send, asked as the
  // reader writes. It is advice — it words the strip and may say beforehand
  // why Run is refused — and is kept with the text it is about: a reading of
  // some other text is never shown, and never stands in for the check a run
  // makes of its own.
  const [advice, setAdvice] = useState<{ sql: string; verdict: ClassifyResponse } | null>(null)
  const targetSql = standing.target?.sql ?? ""
  useEffect(() => {
    if (!targetSql) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      post<ClassifyResponse>(
        `/databases/${id}/classify`,
        { query: targetSql },
        { signal: controller.signal },
      )
        .then((verdict) => {
          if (typeof verdict?.level === "string") setAdvice({ sql: targetSql, verdict })
        })
        .catch(() => {
          // No advice is not a verdict: the run asks for itself.
        })
    }, 400)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [id, targetSql])
  const advised = advice && advice.sql === targetSql ? advice.verdict : undefined

  /* ------------------------------------------------------------- running */

  const refreshHistory = history.refresh
  const refreshSchema = schema.refresh
  const runner = useRunner({
    // However the reader answers, the keyboard goes back to the statement.
    confirm: useCallback(
      (request, closed) =>
        confirm(request, () => {
          closed?.()
          setTimeout(() => editor.current?.focus(), 0)
        }),
      [confirm],
    ),
    onSettled: useCallback(
      (ran) => {
        refreshHistory()
        // A table made, altered or dropped from here is known to the tree and
        // to the completion without the reader asking for the list again.
        if (ran && definesSchema(ran)) {
          described.current.clear()
          refreshSchema()
        }
      },
      [refreshHistory, refreshSchema],
    ),
  })
  const held = runner.runs[tab.id]
  const running = held?.run?.phase === "running"
  const verdict = advised ? gate(advised, runner.asker) : undefined

  const [storedLimit, setLimit] = useViewState(`databases.${id}.query.limit`, DEFAULT_LIMIT)
  const limit = (ROW_LIMITS as readonly number[]).includes(storedLimit)
    ? storedLimit
    : DEFAULT_LIMIT
  const [transaction, setTransaction] = useViewState("databases.query.transaction", false)
  const canTransact = engine.can("transactions")

  const [views, setViews] = useState<Record<string, ResultsViewId>>({})
  const view = views[tab.id] ?? "results"
  const setView = useCallback(
    (next: ResultsViewId, of = tab.id) => setViews((all) => ({ ...all, [of]: next })),
    [tab.id],
  )

  const run = useCallback(
    (all: boolean, rows = limit) => {
      const target = editor.current?.target(all)
      if (!target || running || runner.checking === tab.id) return
      editor.current?.mark(null)
      setView("results")
      void runner.run(tab.id, target, { limit: rows, transaction: transaction && canTransact })
    },
    [limit, running, runner, tab.id, transaction, canTransact, setView],
  )

  const explain = useCallback(
    (analyze: boolean) => {
      const target = editor.current?.target(false)
      if (!target) return
      setView("plan")
      void runner.explain(tab.id, target.sql, analyze)
    },
    [runner, tab.id, setView],
  )

  // A statement that failed is marked where it stands, while the text it was
  // run from is still the text in the editor.
  const showInEditor = useCallback(
    (step: RunStep) => {
      const ran = held?.run
      if (!ran || tab.sql.slice(ran.offset, ran.offset + ran.sql.length) !== ran.sql) {
        notify.info("The text has changed since this ran", {
          description: `The statement was on line ${step.line} when it was sent.`,
        })
        return
      }
      const spans = splitStatements(ran.sql, dialect)
      const span = spans.length === ran.steps.length ? spans[step.index] : undefined
      const start = span?.start ?? 0
      const text = span ? ran.sql.slice(span.start, span.end) : ran.sql
      // The engine's words often name what it tripped on; where they do, that
      // is what is marked, and the whole statement where they do not.
      const at = step.error ? errorPlace(step.error, text, dialect) : null
      editor.current?.mark({
        offset: ran.offset + start + (at?.start ?? 0),
        length: at ? at.length : text.length,
        message: step.error ?? "This statement failed",
      })
      editor.current?.focus()
    },
    [held, tab.sql, dialect],
  )
  const failedAt = held?.run?.phase === "done" ? held.run.finishedAt : undefined
  const failedStep = held?.run && held.run.failed >= 0 ? held.run.steps[held.run.failed] : undefined
  // Marked once for each run, and again when its tab comes back to the front:
  // a tab is a new editor each time it is shown.
  const marked = useRef<{ tab: string; at: number | undefined }>({ tab: "", at: undefined })
  useEffect(() => {
    if (marked.current.tab !== tab.id) marked.current = { tab: tab.id, at: undefined }
    if (!failedAt || marked.current.at === failedAt || !failedStep) return
    marked.current.at = failedAt
    const ran = held?.run
    if (ran && tab.sql.slice(ran.offset, ran.offset + ran.sql.length) === ran.sql) {
      showInEditor(failedStep)
    }
  }, [tab.id, failedAt, failedStep, held, tab.sql, showInEditor])

  const exporter = useResultExport(id)

  /* ------------------------------------------------------ saved queries */

  const canWrite = can("service.control")
  const [naming, setNaming] = useState(false)
  const refreshSaved = saved.refresh

  const save = useCallback(async () => {
    if (!canWrite || !tab.sql.trim()) return
    if (tab.saved === undefined) {
      setNaming(true)
      return
    }
    try {
      const kept = await put<SavedQuery>(`/databases/${id}/queries/${tab.saved}`, { sql: tab.sql })
      setTabs((state) => bindSaved(state, tab.id, kept))
      refreshSaved()
      notify.success(`Saved ${kept.name}`)
    } catch (err) {
      notify.error("Could not save the query", err)
    }
  }, [canWrite, tab, id, setTabs, refreshSaved])

  const renameSaved = useCallback(
    async (query: SavedQuery, name: string) => {
      try {
        const kept = await put<SavedQuery>(`/databases/${id}/queries/${query.id}`, { name })
        setTabs((state) => followSaved(state, { id: kept.id, name: kept.name }))
        refreshSaved()
      } catch (err) {
        notify.error(`Could not rename ${query.name}`, err)
        throw err
      }
    },
    [id, setTabs, refreshSaved],
  )

  const deleteSaved = useCallback(
    (query: SavedQuery, closed: (deleted: boolean) => void) => {
      let deleted = false
      confirm(
        {
          title: "Delete saved query",
          confirmLabel: "Delete",
          subject: {
            mark: <EngineMark engine={engine} size="sm" />,
            name: query.name,
            facts: <FormFact label="Kept for">{conn.name}</FormFact>,
          },
          description: (
            <div className="space-y-3">
              <p>
                The statement is taken off the list for everybody who opens this connection. A tab
                that holds it keeps its text.
              </p>
              <Statement sql={query.sql} placeholder="" />
            </div>
          ),
          action: async () => {
            await del(`/databases/${id}/queries/${query.id}`)
            deleted = true
            setTabs((state) => followSaved(state, { id: query.id, gone: true }))
            refreshSaved()
            notify.success(`Deleted ${query.name}`)
            return "reported"
          },
        },
        () => closed(deleted),
      )
    },
    [confirm, engine, conn.name, id, setTabs, refreshSaved],
  )

  /* ---------------------------------------------------------------- tabs */

  // A statement from the history or the snippets: the tab that already holds
  // exactly it comes to the front, else it opens in one of its own, named for
  // what it is.
  const openStatement = useCallback(
    (sql: string, title?: string) => {
      const named = title ?? titleOf(sql)
      if (!place({ sql }, (state) => handOver(state, sql, named))) return
      if (!railBeside) setRailOver(false)
    },
    [place, railBeside],
  )

  const close = useCallback(
    (tabId: string) => {
      const at = tabs.tabs.findIndex((entry) => entry.id === tabId)
      const closed = tabs.tabs[at]
      if (!closed) return
      // A statement still out is stopped with its tab: nothing is left running where nobody can see it.
      runner.cancel(tabId)
      setTabs((state) => closeTab(state, tabId))
      const forget = () => {
        runner.forget(tabId)
        forgetDocument(`${id}:${tabId}`)
      }
      // A draft that was never kept is the one thing closing loses: it can be put back.
      if (closed.sql.trim() && (closed.saved === undefined || isChanged(closed))) {
        // What it ran is kept while it can still be put back, and let go after.
        const later = setTimeout(forget, 12_000)
        notify.info(`Closed ${closed.title}`, {
          description: firstLine(closed.sql, 80),
          duration: 10_000,
          action: {
            label: "Undo",
            onClick: () => {
              clearTimeout(later)
              setTabs((state) => restoreTab(state, closed, at))
            },
          },
        })
      } else forget()
    },
    [tabs.tabs, setTabs, runner, id],
  )

  const insertName = useCallback(
    (name: string) => editor.current?.insert(quoteName(name, dialect, RESERVED)),
    [dialect],
  )
  const openTable = useCallback(
    (table: SchemaTable) => {
      const relation = qualifiedName(
        table.schema,
        table.name,
        dialect,
        schema.model.defaultSchema,
        RESERVED,
      )
      openStatement(dialect.firstRows(relation, 100), table.name)
    },
    [dialect, schema.model.defaultSchema, openStatement],
  )

  const runningTabs = useMemo(
    () =>
      new Set(
        Object.entries(runner.runs)
          .filter(
            ([, entry]) => entry.run?.phase === "running" || entry.explain?.phase === "running",
          )
          .map(([key]) => key),
      ),
    [runner.runs],
  )

  // One diagnostic of each kind, for a tab with nothing in it yet.
  const starters = useMemo(() => {
    const seen = new Set<string>()
    return snippets
      .filter((snippet) => !seen.has(snippet.group) && seen.add(snippet.group))
      .slice(0, 4)
  }, [snippets])

  const canRun = runner.asker.canRun
  const canAnalyze = canRun && engine.can("explainAnalyze")
  const empty = tab.sql.trim() === ""

  return (
    <SectionFrame section="query">
      {/* One frame around the whole workbench: the rail and the editor are
          columns of one working surface, with a hairline between them. */}
      <div
        ref={frame}
        data-slot="sql-workbench"
        style={{ "--jd-query-rail": `${railColumn}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        {railShown && (
          <div
            className={cn(
              "flex min-h-0 flex-col border-hairline bg-card",
              railBeside
                ? "relative w-(--jd-query-rail) shrink-0 border-r"
                : "absolute inset-0 z-30",
            )}
          >
            <QueryRail
              view={railView}
              onView={setRailView}
              saved={saved}
              history={history}
              schema={schema}
              snippets={snippets}
              canWrite={canWrite}
              openSaved={tab.saved}
              onOpenSaved={(query) => {
                const opening = { sql: query.sql, title: query.name, saved: query.id }
                if (!place(opening, (state) => openTab(state, opening))) return
                if (!railBeside) setRailOver(false)
              }}
              onRenameSaved={renameSaved}
              onDeleteSaved={deleteSaved}
              onOpenStatement={openStatement}
              onInsert={(name) => {
                insertName(name)
                if (!railBeside) setRailOver(false)
              }}
              onOpenTable={openTable}
              picked={picked}
              onPickSchema={(name) => select({ schema: name })}
            />
            {railBeside ? (
              <ResizeHandle
                side="left"
                label="Rail width"
                value={railColumn}
                min={RAIL.min}
                max={railMax}
                onChange={(px, commit) => setRailWidth(clamp(px, RAIL.min, railMax), commit)}
                onReset={resetRailWidth}
                className="absolute inset-y-0 -right-1 z-20"
              />
            ) : (
              <div className="shrink-0 border-t border-hairline bg-surface-header p-2">
                <Button
                  size="sm"
                  variant="outline"
                  className="w-full"
                  onClick={() => setRailOver(false)}
                >
                  Back to the editor
                </Button>
              </div>
            )}
          </div>
        )}

        <div ref={main} className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div className="flex h-10 shrink-0 items-stretch border-b border-hairline bg-surface-header">
            <IconAction
              label={railShown ? "Hide the rail" : "Show the rail"}
              aria-pressed={railShown}
              className="mx-1.5 size-7 shrink-0 self-center max-sm:size-8"
              onClick={() => (railBeside ? setRailPinned(!railPinned) : setRailOver(!railOver))}
            >
              {railShown ? <SidebarLeftClose /> : <SidebarLeftOpen />}
            </IconAction>
            <TabStrip
              idBase={ids}
              panelId={panelId}
              tabs={tabs.tabs}
              active={tab.id}
              running={runningTabs}
              onShow={(tabId) => setTabs((state) => showTab(state, tabId))}
              onClose={close}
              onNew={() => {
                setTabs(newTab)
                // A new tab is opened to be written in.
                setTimeout(() => editor.current?.focus(), 0)
              }}
              onRename={(tabId, title) => {
                const renamed = tabs.tabs.find((entry) => entry.id === tabId)
                if (!renamed || !title.trim() || title.trim() === renamed.title) return
                // The name of a saved query's tab is the saved query's name.
                if (renamed.saved !== undefined && canWrite) {
                  const query = saved.data?.find((entry) => entry.id === renamed.saved)
                  if (query) return void renameSaved(query, title.trim()).catch(() => {})
                }
                setTabs((state) => renameTab(state, tabId, title))
              }}
              full={tabs.tabs.length >= MAX_TABS}
            />
          </div>

          <div
            role="tabpanel"
            id={panelId}
            aria-labelledby={tabElementId(ids, tab.id)}
            style={{ flex: `${1 - share} 1 0%` }}
            className="flex min-h-24 min-w-0 flex-col"
          >
            <SqlEditor
              key={tab.id}
              ref={editor}
              docKey={`${id}:${tab.id}`}
              label={tab.title}
              className="min-h-0 flex-1"
              value={tab.sql}
              onChange={(sql) => setTabs((state) => setTabSql(state, tab.id, sql))}
              language={engine.editor}
              dialect={dialect}
              schema={schema.model}
              vocabulary={vocabulary}
              describe={describe}
              onLeave={(direction) =>
                frame.current
                  ?.querySelector<HTMLElement>(
                    direction > 0
                      ? '[data-slot="query-commands"] button:not(:disabled)'
                      : '[data-slot="query-tabs"] [role="tab"][aria-selected="true"]',
                  )
                  ?.focus()
              }
              onRun={run}
              onSave={() => void save()}
              onStanding={onStanding}
            />
          </div>

          <CommandStrip
            compact={compact}
            standing={standing}
            advice={advised}
            verdict={verdict}
            run={running ? held?.run : undefined}
            checking={runner.checking === tab.id}
            canRun={canRun}
            canAnalyze={canAnalyze}
            canCancel={engine.can("queryCancel")}
            canTransact={canTransact}
            canSave={canWrite}
            saved={tab.saved !== undefined}
            changed={isChanged(tab)}
            transaction={transaction}
            onTransaction={setTransaction}
            limit={limit}
            onLimit={setLimit}
            onRun={run}
            onCancel={() => runner.cancel(tab.id)}
            onExplain={explain}
            onFormat={() => editor.current?.format()}
            onSave={() => void save()}
            empty={empty}
          />

          <div
            style={{ flex: `${share} 1 0%` }}
            className="relative flex min-h-32 min-w-0 flex-col"
          >
            <SplitHandle
              label="Height of the results"
              value={share}
              min={SPLIT.min}
              max={SPLIT.max}
              onChange={setSplit}
              onReset={() => setSplit(SPLIT.fallback)}
              measure={() => main.current?.getBoundingClientRect().height ?? 1}
              className="absolute inset-x-0 -top-1 z-20"
            />
            <Results
              held={held}
              checking={runner.checking === tab.id}
              view={view}
              onView={(next) => setView(next)}
              canRun={canRun}
              canAnalyze={canAnalyze}
              noPlanWhy={
                empty
                  ? "Write a statement, then Explain shows how the engine would run it."
                  : undefined
              }
              onRun={() => run(false)}
              onCancel={() => runner.cancel(tab.id)}
              compact={compact}
              onFetchMore={(step, rows) => {
                // The one statement is asked again; the others keep what they returned.
                const ran = held?.run
                if (ran) void runner.fetchMore(tab.id, ran, step, rows)
              }}
              onExplain={explain}
              onExport={(sql, format, rows) => void exporter.run(sql, format, rows)}
              exporting={exporter.running}
              onShowInEditor={showInEditor}
              empty={empty}
              starters={starters}
              onStart={(snippet) => openStatement(snippet.sql, snippet.title)}
              refusedWhy={verdict && !verdict.run ? verdict.why : undefined}
            />
          </div>
        </div>
      </div>

      {naming && (
        <SaveDialog
          onClose={() => {
            setNaming(false)
            // The dialog was opened from the editor (Ctrl+S) as often as from
            // the strip: the keyboard goes back to the statement.
            setTimeout(() => editor.current?.focus(), 0)
          }}
          sql={tab.sql}
          suggested={tab.title.startsWith("Query ") ? "" : tab.title}
          taken={saved.data?.map((entry) => entry.name) ?? []}
          onSave={async (name) => {
            const kept = await post<SavedQuery>(`/databases/${id}/queries`, { name, sql: tab.sql })
            setTabs((state) => bindSaved(state, tab.id, kept))
            refreshSaved()
            setRailView("saved")
            notify.success(`Saved ${kept.name}`)
          }}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}

/** Names the statement in the editor and keeps it for this connection. */
function SaveDialog({
  onClose,
  sql,
  suggested,
  taken,
  onSave,
}: {
  onClose: () => void
  sql: string
  suggested: string
  taken: string[]
  onSave: (name: string) => Promise<void>
}) {
  const [name, setName] = useState(suggested)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const typed = name.trim()
  const twin = taken.some((entry) => entry.toLowerCase() === typed.toLowerCase())
  const submit = async () => {
    if (!typed || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await onSave(typed)
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title="Save this query"
      description="Name the statement to keep it for this connection"
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            Kept for everybody who opens this connection.
          </p>
          <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="save-query" pending={busy} disabled={!typed}>
            Save
          </Button>
        </>
      }
    >
      <form
        id="save-query"
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <Field
          label="Name"
          htmlFor="saved-query-name"
          error={error}
          hint={
            twin ? "Another saved query has this name; both are kept." : "Up to 200 characters."
          }
        >
          <Input
            id="saved-query-name"
            autoFocus
            maxLength={200}
            value={name}
            placeholder="Orders waiting to ship"
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Statement sql={sql} placeholder="" />
      </form>
    </Modal>
  )
}
