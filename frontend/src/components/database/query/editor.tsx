"use client"

import { useEffect, useImperativeHandle, useRef } from "react"
import { cn } from "@/lib/utils"
import { CodeEditor } from "@/components/code-editor"
import type { SchemaModel, Vocabulary } from "@/components/database/query/completion"
import type { Dialect } from "@/components/database/query/dialect"
import { formatSql } from "@/components/database/query/format"
import {
  completeFrom,
  loadMonaco,
  provideCompletion,
  whenEditor,
  type Monaco,
  type MonacoEditor,
} from "@/components/database/query/monaco"
import {
  runTarget,
  splitStatements,
  statementAt,
  type RunTarget,
} from "@/components/database/query/sql-text"

/** What the page can ask of the editor. */
export interface SqlEditorHandle {
  focus: () => void
  /** What a run would send now: the selection, else the statement under the cursor; everything when `all`. */
  target: (all?: boolean) => RunTarget | null
  /** Writes text at the cursor, over the selection if there is one. */
  insert: (text: string) => void
  /** Lays out the selection, or the whole text. */
  format: () => void
  /** Marks a stretch of the text as where a statement failed, until the text changes. */
  mark: (problem: Problem | null) => void
}

type Problem = { offset: number; length: number; message: string }

const MARKS = "jd-sql"

/** Writes the mark on a document, or takes it off. */
function setMark(held: { editor: MonacoEditor; monaco: Monaco }, problem: Problem | null) {
  const model = held.editor.getModel()
  if (!model) return
  if (!problem) {
    held.monaco.editor.setModelMarkers(model, MARKS, [])
    return
  }
  const from = model.getPositionAt(problem.offset)
  const to = model.getPositionAt(problem.offset + problem.length)
  held.monaco.editor.setModelMarkers(model, MARKS, [
    {
      severity: held.monaco.MarkerSeverity.Error,
      message: problem.message,
      startLineNumber: from.lineNumber,
      startColumn: from.column,
      endLineNumber: to.lineNumber,
      endColumn: to.column,
    },
  ])
  held.editor.revealLineInCenterIfOutsideViewport(from.lineNumber)
}

/** Where the cursor stands among the statements, for the strip beside Run. */
export interface Standing {
  /** What Run would send. */
  target: RunTarget | null
  /** Which statement the cursor is on, 1-based, and how many there are. */
  at: number
  of: number
}

/**
 * The statement editor: the shared Monaco editor, with what a query needs of
 * it — a key that runs, the statement under the cursor marked in the gutter,
 * names completed from this connection's schema, and a way for the rail to
 * write a name at the cursor.
 *
 * It is keyed by tab above: a tab is a document of its own, with its own
 * undo, and one tab's text is never replayed into another's history.
 */
export function SqlEditor({
  ref,
  value,
  onChange,
  language,
  dialect,
  schema,
  vocabulary,
  onRun,
  onSave,
  onStanding,
  className,
}: {
  ref?: React.Ref<SqlEditorHandle>
  value: string
  onChange: (value: string) => void
  /** The Monaco grammar of the engine. */
  language: string
  dialect: Dialect
  schema: SchemaModel
  vocabulary: Vocabulary
  /** Ctrl/Cmd+Enter (and with Shift: everything). */
  onRun: (all: boolean) => void
  onSave?: () => void
  onStanding: (standing: Standing) => void
  className?: string
}) {
  const host = useRef<HTMLDivElement>(null)
  const attached = useRef<{ editor: MonacoEditor; monaco: Monaco } | null>(null)
  // A mark asked for before the editor exists — a tab brought back to the
  // front with a failed statement — is set as soon as it does.
  const pending = useRef<Problem | null>(null)
  // The editor's listeners are registered once; what they call is read at the
  // moment they fire, so they never run a handler from an earlier render.
  const latest = useRef({ dialect, schema, vocabulary, onRun, onStanding })
  useEffect(() => {
    latest.current = { dialect, schema, vocabulary, onRun, onStanding }
  })

  const targetOf = (all = false): RunTarget | null => {
    const held = attached.current
    const model = held?.editor.getModel()
    if (!held || !model) return null
    const selection = held.editor.getSelection()
    const position = held.editor.getPosition()
    return runTarget(
      model.getValue(),
      selection && !selection.isEmpty()
        ? {
            start: model.getOffsetAt(selection.getStartPosition()),
            end: model.getOffsetAt(selection.getEndPosition()),
          }
        : null,
      position ? model.getOffsetAt(position) : 0,
      latest.current.dialect,
      all,
    )
  }
  const target = useRef(targetOf)
  useEffect(() => {
    target.current = targetOf
  })

  useEffect(() => {
    const node = host.current
    if (!node) return
    let stopped = false
    let stop = () => {}
    void loadMonaco().then((monaco) => {
      if (stopped) return
      const waiting = whenEditor(monaco, node, (editor) => {
        const model = editor.getModel()
        if (!model) return
        attached.current = { editor, monaco }
        if (pending.current) setMark(attached.current, pending.current)
        provideCompletion(monaco, language)
        const untie = completeFrom(model.uri.toString(), () => ({
          model: latest.current.schema,
          dialect: latest.current.dialect,
          vocabulary: latest.current.vocabulary,
        }))

        const run = editor.addAction({
          id: "jd.sql.run",
          label: "Run the statement",
          keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter],
          run: () => latest.current.onRun(false),
        })
        const runAll = editor.addAction({
          id: "jd.sql.runAll",
          label: "Run everything",
          keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.Enter],
          run: () => latest.current.onRun(true),
        })
        const lay = editor.addAction({
          id: "jd.sql.format",
          label: "Format the statement",
          keybindings: [monaco.KeyMod.Shift | monaco.KeyMod.Alt | monaco.KeyCode.KeyF],
          run: () => formatIn(editor, latest.current.dialect),
        })

        // The statement Run would send, marked in the gutter where there is
        // more than one to choose between.
        const gutter = editor.createDecorationsCollection([])
        const stand = () => {
          const text = model.getValue()
          const position = editor.getPosition()
          const offset = position ? model.getOffsetAt(position) : 0
          const spans = splitStatements(text, latest.current.dialect)
          const span = statementAt(spans, offset)
          const picked = target.current(false)
          const marked = picked && spans.length > 1 ? picked : null
          gutter.set(
            marked
              ? [
                  {
                    range: monaco.Range.fromPositions(
                      model.getPositionAt(marked.offset),
                      model.getPositionAt(marked.offset + marked.sql.length),
                    ),
                    options: {
                      isWholeLine: true,
                      // The brand is "where you are": the statement that will run.
                      linesDecorationsClassName: "ml-1 border-l-2 border-brand",
                    },
                  },
                ]
              : [],
          )
          latest.current.onStanding({
            target: picked,
            at: span ? spans.indexOf(span) + 1 : 0,
            of: spans.length,
          })
        }
        const onCursor = editor.onDidChangeCursorSelection(stand)
        const onText = editor.onDidChangeModelContent(() => {
          // A mark says where the last run failed; once the text changes it points at nothing.
          pending.current = null
          monaco.editor.setModelMarkers(model, MARKS, [])
          stand()
        })
        stand()

        stop = () => {
          untie()
          run.dispose()
          runAll.dispose()
          lay.dispose()
          onCursor.dispose()
          onText.dispose()
          gutter.clear()
          attached.current = null
        }
      })
      stop = waiting
    })
    return () => {
      stopped = true
      stop()
    }
  }, [language])

  useImperativeHandle(
    ref,
    () => ({
      focus: () => attached.current?.editor.focus(),
      target: (all) => target.current(all),
      insert: (text) => {
        const editor = attached.current?.editor
        const selection = editor?.getSelection()
        if (!editor || !selection) return
        editor.executeEdits("jd.sql.insert", [{ range: selection, text, forceMoveMarkers: true }])
        editor.focus()
      },
      format: () => {
        const editor = attached.current?.editor
        if (editor) formatIn(editor, latest.current.dialect)
      },
      mark: (problem) => {
        pending.current = problem
        if (attached.current) setMark(attached.current, problem)
      },
    }),
    [],
  )

  return (
    <div
      ref={host}
      data-slot="sql-editor"
      className={cn("flex min-h-0 min-w-0 flex-col", className)}
    >
      <CodeEditor
        className="min-h-0 flex-1"
        language={language}
        value={value}
        onChange={onChange}
        onSave={onSave}
      />
    </div>
  )
}

/** Lays out the selection, or everything, as one edit the reader can undo. */
function formatIn(editor: MonacoEditor, dialect: Dialect) {
  const model = editor.getModel()
  if (!model) return
  const selection = editor.getSelection()
  const partial = selection && !selection.isEmpty()
  const range = partial ? selection : model.getFullModelRange()
  const before = model.getValueInRange(range)
  const laid = formatSql(before, dialect)
  // A selection inside a line keeps the line it ends on.
  const after = partial ? laid.replace(/\n$/, "") : laid
  if (after === before) return
  editor.pushUndoStop()
  editor.executeEdits("jd.sql.format", [{ range, text: after }])
  editor.pushUndoStop()
}
