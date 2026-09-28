"use client"

import { useEffect, useId, useState } from "react"
import { useSearchParams } from "next/navigation"
import {
  AcronymJson,
  Clock,
  Download,
  FloppyDisk,
  Layout,
  Monorepo,
  Play,
  ShieldOff,
  Star,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { del, get, post } from "@/lib/api"
import { resultToCSV } from "@/lib/db-export"
import { cn, ringSafeScroll } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import type {
  DbConnection,
  DbHistoryEntry,
  DbOutline,
  DbSavedQuery,
  QueryResult,
  QueryRisk,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Panel, PanelBody, PanelFooter, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

import { ResultGrid } from "@/components/database/result-grid"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Field } from "@/components/form"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/** The Query tab: the SQL editor, live risk classification, a per-connection
 *  history and saved snippets, and a result you can export. */
export function QueryTab({ conn, confirm }: { conn: DbConnection; confirm: ConfirmFn }) {
  const { can } = useAuth()
  const params = useSearchParams()
  // A statement handed over in the URL — the diagram's "query this table" —
  // opens in the editor; it is not run until Run is pressed. Otherwise the
  // editor holds what was last typed against this connection: a query is
  // the slowest thing on the page to write and the easiest to lose.
  const [sql, setSql] = useSessionState(
    `databases.${conn.id}.query.sql`,
    "SELECT 1;",
    params.get("sql") || undefined,
  )
  const [risk, setRisk] = useState<QueryRisk | null>(null)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [plan, setPlan] = useState<QueryResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [saveOpen, setSaveOpen] = useState(false)
  const [side, setSide] = useViewState("db.query.side", "history")

  const saved = usePoll(
    (signal) => get<DbSavedQuery[]>(`/databases/${conn.id}/queries`, undefined, signal),
    0,
    [conn.id],
  )
  const history = usePoll(
    (signal) => get<DbHistoryEntry[]>(`/databases/${conn.id}/history`, { limit: 50 }, signal),
    0,
    [conn.id],
  )
  // The schema, fetched once per connection, so completion knows what exists.
  // A failure here is silent: an editor without suggestions still runs SQL, and
  // an error toast for a convenience nobody asked for is noise.
  const outline = usePoll(
    (signal) => get<DbOutline>(`/databases/${conn.id}/outline`, undefined, signal),
    0,
    [conn.id],
  )

  // Classification happens as you type, so the warning appears before the
  // statement is ever sent to the database.
  useEffect(() => {
    const timer = setTimeout(() => {
      post<QueryRisk>(`/databases/${conn.id}/classify`, { query: sql })
        .then(setRisk)
        .catch(() => setRisk(null))
    }, 400)
    return () => clearTimeout(timer)
  }, [sql, conn.id])

  const execute = async (confirmText?: string) => {
    setBusy(true)
    try {
      const res = await post<{ result: QueryResult; risk: QueryRisk }>(
        `/databases/${conn.id}/query`,
        { query: sql, maxRows: 500 },
        { confirm: confirmText },
      )
      setResult(res.result)
      setPlan(null)
      history.refresh()
      notify.success(
        res.result.columns.length
          ? `${plural(res.result.rowCount, "row")} in ${res.result.duration}`
          : `${plural(res.result.rowsAffected, "row")} affected`,
      )
    } catch (err) {
      history.refresh()
      notify.error("Query failed", err)
      throw err
    } finally {
      setBusy(false)
    }
  }

  const run = () => {
    if (risk?.destructive) {
      confirm({
        title: "Run destructive statement",
        // Only a critical statement is typed for — a DROP, a TRUNCATE, or an
        // UPDATE/DELETE with no WHERE. "high" is a scoped write, which is the
        // ordinary work of a SQL console; making somebody type "run high"
        // dozens of times a sitting is how the phrase stops being read.
        phrase: risk.level === "critical" ? `run ${risk.level}` : undefined,
        confirmLabel: "Run it",
        description: (
          <>
            <p className="text-destructive">This statement {risk.reasons.join(", ")}.</p>
            <Well className="mt-2 max-h-32">{sql}</Well>
          </>
        ),
        action: (c) => execute(c),
      })
      return
    }
    execute().catch(() => undefined)
  }

  // Planning is not running: every dialect describes the statement without
  // executing it, which is why this needs no confirmation even for a DELETE.
  const explain = async () => {
    setBusy(true)
    try {
      const res = await post<{ result: QueryResult }>(`/databases/${conn.id}/explain`, {
        query: sql,
      })
      setPlan(res.result)
      setResult(null)
    } catch (err) {
      notify.error("Could not plan the statement", err)
    } finally {
      setBusy(false)
    }
  }

  const saveSnippet = async (name: string) => {
    await post(`/databases/${conn.id}/queries`, { name, sql })
    notify.success(`Saved "${name}"`)
    saved.refresh()
  }
  const deleteSnippet = (q: DbSavedQuery) =>
    confirm({
      title: "Delete saved query",
      description: (
        <p>
          Removes the snippet <b>{q.name}</b>.
        </p>
      ),
      action: async () => {
        await del(`/databases/${conn.id}/queries/${q.id}`)
        saved.refresh()
      },
    })

  const exportResult = (format: "csv" | "json") => {
    if (!result) return
    const text = format === "csv" ? resultToCSV(result) : toJSON(result)
    const blob = new Blob([text], { type: format === "csv" ? "text/csv" : "application/json" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `query-result.${format}`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {risk?.destructive && (
        <Notice tone="danger" icon={ShieldOff} title={`Destructive statement (${risk.level})`}>
          {risk.reasons.join(" · ")}
        </Notice>
      )}

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem] [&>*]:min-w-0">
        <Panel>
          <PanelHeader title="SQL" />
          <PanelBody flush>
            <CodeEditor
              className="h-56"
              language="sql"
              value={sql}
              onChange={setSql}
              completions={outline.data?.tables}
            />
          </PanelBody>
          <PanelFooter>
            <Button
              size="sm"
              onClick={run}
              disabled={busy || !can("service.control")}
              pending={busy}
            >
              <Play className="size-3.5" />
              Run
            </Button>
            <Button size="sm" variant="outline" onClick={explain} disabled={busy || !sql.trim()}>
              <Monorepo className="size-3.5" />
              Explain
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setSaveOpen(true)}
              disabled={!can("service.control") || !sql.trim()}
            >
              <FloppyDisk className="size-3.5" />
              Save
            </Button>
            {risk && !risk.destructive && <Tag>{risk.level}</Tag>}
            {outline.data && (
              <span className="text-hint text-muted-foreground">
                {plural(Object.keys(outline.data.tables).length, "table")} available to autocomplete
              </span>
            )}
            {!can("service.control") && (
              <span className="text-xs text-muted-foreground">
                Your role can browse but not execute statements.
              </span>
            )}
          </PanelFooter>
        </Panel>

        <Panel className="min-h-0">
          <Tabs value={side} onValueChange={setSide} className="gap-0">
            <PanelHeader className="py-2">
              <TabsList className="h-8">
                <TabsTrigger value="history" className="gap-1 text-xs">
                  <Clock className="size-3.5" />
                  History
                </TabsTrigger>
                <TabsTrigger value="saved" className="gap-1 text-xs">
                  <Star className="size-3.5" />
                  Saved
                </TabsTrigger>
              </TabsList>
            </PanelHeader>
            <PanelBody flush>
              <TabsContent value="history" className="m-0">
                <div
                  className={cn(
                    "max-h-[16rem] divide-y divide-hairline overflow-y-auto",
                    ringSafeScroll,
                  )}
                >
                  {history.data?.length === 0 && <EmptyNote>No statements yet.</EmptyNote>}
                  {history.data?.map((h) => (
                    <button
                      key={h.id}
                      onClick={() => setSql(h.sql)}
                      className="flex w-full flex-col gap-0.5 px-3 py-2 text-left hover:bg-accent"
                    >
                      <span className="truncate font-mono text-hint">{h.sql}</span>
                      <span className="flex items-center gap-1.5 text-micro text-muted-foreground">
                        <span
                          className={cn(
                            "size-1.5 rounded-full",
                            h.success ? "bg-success" : "bg-destructive",
                          )}
                        />
                        {h.risk} · {plural(h.rowCount, "row")} · {h.durationMs}ms
                      </span>
                    </button>
                  ))}
                </div>
              </TabsContent>
              <TabsContent value="saved" className="m-0">
                <div
                  className={cn(
                    "max-h-[16rem] divide-y divide-hairline overflow-y-auto",
                    ringSafeScroll,
                  )}
                >
                  {saved.data?.length === 0 && <EmptyNote>No saved queries.</EmptyNote>}
                  {saved.data?.map((q) => (
                    <div
                      key={q.id}
                      className="group flex items-center gap-1 px-3 py-2 hover:bg-accent"
                    >
                      <button
                        onClick={() => setSql(q.sql)}
                        className="flex min-w-0 flex-1 flex-col gap-0.5 text-left"
                      >
                        <span className="truncate text-xs font-medium">{q.name}</span>
                        <span className="truncate font-mono text-micro text-muted-foreground">
                          {q.sql}
                        </span>
                      </button>
                      {can("service.control") && (
                        <IconAction
                          label={`Delete the saved query ${q.name}`}
                          reveal
                          className="shrink-0 text-destructive"
                          onClick={() => deleteSnippet(q)}
                        >
                          <Trash />
                        </IconAction>
                      )}
                    </div>
                  ))}
                </div>
              </TabsContent>
            </PanelBody>
          </Tabs>
        </Panel>
      </div>

      {plan && (
        <Panel>
          <PanelHeader title="Query plan" />
          <PanelBody flush>
            <ResultGrid result={plan} />
          </PanelBody>
        </Panel>
      )}

      {result && (
        <Panel>
          <PanelHeader
            title="Result"
            actions={
              result.columns.length > 0 && (
                <>
                  <Button size="sm" variant="ghost" onClick={() => exportResult("csv")}>
                    <Download className="size-3.5" />
                    CSV
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => exportResult("json")}>
                    <AcronymJson className="size-3.5" />
                    JSON
                  </Button>
                </>
              )
            }
          />
          <PanelBody flush>
            <ResultGrid
              result={result}
              emptyTitle="The statement ran and matched nothing"
              emptyDescription="Not an error — the columns below are the shape it would have returned."
            />
          </PanelBody>
        </Panel>
      )}
      {!result && !plan && <EmptyState icon={Layout} title="Run a statement to see results" />}

      <SaveDialog open={saveOpen} onOpenChange={setSaveOpen} onSave={saveSnippet} />
    </div>
  )
}

function SaveDialog({
  open,
  onOpenChange,
  onSave,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onSave: (name: string) => Promise<void>
}) {
  const id = useId()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      await onSave(name)
      onOpenChange(false)
      setName("")
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title="Save query"
      description="Keeps the statement in the editor against this connection, under a name."
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!name.trim() || busy} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <Field
        label="Name"
        htmlFor={`${id}-name`}
        hint="Saved against this connection, for everyone who can open it."
      >
        <Input
          id={`${id}-name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && !busy && void save()}
          placeholder="Active users last 30 days"
          autoFocus
        />
      </Field>
    </Modal>
  )
}

// toCSV / toJSON build a downloadable file from an already-fetched result, so a
// query result the operator is looking at can be saved without re-running it.
function toJSON(result: QueryResult): string {
  const objs = result.rows.map((row) => {
    const o: Record<string, unknown> = {}
    result.columns.forEach((c, i) => (o[c] = row[i]))
    return o
  })
  return JSON.stringify(objs, null, 2)
}
