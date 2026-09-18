"use client"

import { useEffect, useRef } from "react"
import dynamic from "next/dynamic"
import { cn } from "@/lib/utils"
import { Spinner } from "@/components/state"

// Monaco pulls in a large worker bundle and touches `window`, so it is loaded
// only when an editor is actually opened.
//
// Where it is loaded *from* is the part that matters here. @monaco-editor/react
// resolves the editor at runtime and its default source is a CDN, which made
// the SQL editor the one thing in this product that needed the operator's
// browser to reach the public internet — in a dashboard whose entire security
// story is a closed network perimeter, and which is normally reached down a
// Tailscale or SSH tunnel. With the CDN unreachable the Query tab sat at
// "Loading…" for ever: no error, no fallback, and no way to type a statement,
// while Run happily executed a value nobody could see. It also told a third
// party, on every page open, that this dashboard exists.
//
// `scripts/sync-monaco.mjs` copies the runtime into public/monaco at build
// time and this points the loader at it, so the editor comes from the same
// origin as everything else the dashboard serves.
const MonacoEditor = dynamic(
  () =>
    import("@monaco-editor/react").then(async (mod) => {
      const { loader } = await import("@monaco-editor/react")
      loader.config({ paths: { vs: `${window.location.origin}/monaco/vs` } })
      return mod
    }),
  {
    ssr: false,
    loading: () => (
      <div className="flex h-full items-center justify-center">
        <Spinner className="size-5 text-muted-foreground" />
      </div>
    ),
  },
)

/**
 * Monaco, wired to the dashboard's palette.
 *
 * Three places embedded the editor and all three hard-coded `theme="vs-dark"`,
 * which put a slate-grey slab inside a card in every one of the three light
 * palettes. Monaco has no notion of our tokens, so the fix is in two parts:
 * pick its built-in light or dark base from the active mode, and let the
 * container behind it own the ground (see `.monaco-host` in globals.css) so
 * the editor blends into whatever surface it was dropped onto.
 */
export function CodeEditor({
  value,
  onChange,
  language,
  readOnly,
  className,
  minHeight,
  completions,
  wordWrap,
  minimap,
  fontSize = 13,
  onSave,
  onCursorChange,
  onFormat,
}: {
  value: string
  onChange?: (value: string) => void
  language?: string
  readOnly?: boolean
  className?: string
  minHeight?: string
  /**
   * Names to offer as completions, as a parent-to-children map — tables to
   * their columns for SQL. Kept as a plain map rather than a Monaco type so
   * this component does not drag the editor's types into every caller.
   */
  completions?: Record<string, string[]>
  wordWrap?: boolean
  minimap?: boolean
  fontSize?: number
  /**
   * Ctrl+S / Cmd+S. Bound inside Monaco rather than on the window, because the
   * editor swallows the keystroke first — a document with focus in the editor
   * would otherwise trigger the browser's own "save page" dialog.
   */
  onSave?: () => void
  onCursorChange?: (position: { line: number; column: number; selected: number }) => void
  /** Called with a function that formats the document, once the editor exists. */
  onFormat?: (format: (() => void) | null) => void
}) {
  // The save handler is read through a ref for the same reason the completion
  // schema is: the command is registered once on mount and would otherwise
  // keep calling the first render's closure, saving the file as it was when
  // the editor opened.
  const saveRef = useRef(onSave)
  useEffect(() => {
    saveRef.current = onSave
  }, [onSave])
  const cursorRef = useRef(onCursorChange)
  useEffect(() => {
    cursorRef.current = onCursorChange
  }, [onCursorChange])
  // Held in a ref so the provider registered on mount always reads the current
  // schema. Re-registering on every change would stack providers and offer each
  // table name once per registration.
  const completionsRef = useRef(completions)
  useEffect(() => {
    completionsRef.current = completions
  }, [completions])

  return (
    <div className={cn("monaco-host min-h-0", className)} style={{ minHeight }}>
      <MonacoEditor
        height="100%"
        theme="vs-dark"
        language={language}
        value={value}
        onChange={(v) => onChange?.(v ?? "")}
        onMount={(editor, monaco) => {
          editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveRef.current?.())
          editor.onDidChangeCursorSelection(() => {
            const position = editor.getPosition()
            const selection = editor.getSelection()
            const model = editor.getModel()
            if (!position || !cursorRef.current) return
            cursorRef.current({
              line: position.lineNumber,
              column: position.column,
              selected:
                selection && model && !selection.isEmpty()
                  ? model.getValueInRange(selection).length
                  : 0,
            })
          })
          onFormat?.(() => {
            void editor.getAction("editor.action.formatDocument")?.run()
          })
          if (!completions || !language) return
          registerSchemaCompletions(monaco, language, completionsRef)
        }}
        options={{
          readOnly,
          minimap: { enabled: minimap ?? false },
          wordWrap: wordWrap ? "on" : "off",
          fontSize,
          lineHeight: Math.round(fontSize * 1.55),
          bracketPairColorization: { enabled: true },
          stickyScroll: { enabled: true },
          rulers: [],
          scrollBeyondLastLine: false,
          automaticLayout: true,
          tabSize: 2,
          renderLineHighlight: "none",
          renderWhitespace: "selection",
          padding: { top: 12, bottom: 12 },
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
          overviewRulerLanes: 0,
        }}
      />
    </div>
  )
}

/**
 * Registers a schema-aware completion provider once per language.
 *
 * Monaco's providers are global to the language rather than scoped to an editor
 * instance, so registering one per mount would leave every previous editor's
 * provider attached and offer each table name once per editor ever opened. One
 * registration reading from a module-level slot is what keeps the list to one
 * copy, and writing the current editor's schema into that slot on mount is what
 * makes the suggestions follow the connection being looked at.
 */
const activeSchema: Record<string, () => Record<string, string[]> | undefined> = {}

function registerSchemaCompletions(
  monaco: typeof import("monaco-editor"),
  language: string,
  ref: { current?: Record<string, string[]> },
) {
  const first = !(language in activeSchema)
  activeSchema[language] = () => ref.current
  if (!first) return

  monaco.languages.registerCompletionItemProvider(language, {
    provideCompletionItems(model, position) {
      const tables = activeSchema[language]?.()
      if (!tables) return { suggestions: [] }
      const word = model.getWordUntilPosition(position)
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: word.startColumn,
        endColumn: word.endColumn,
      }
      const line = model.getValueInRange({
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: 1,
        endColumn: position.column,
      })

      // After `table.` only that table's columns make sense. Offering the whole
      // schema there is what makes most SQL completion useless.
      const qualified = /([A-Za-z_][\w$]*)\.\w*$/.exec(line)
      if (qualified) {
        const cols = tables[qualified[1]] ?? []
        return {
          suggestions: cols.map((c) => ({
            label: c,
            kind: monaco.languages.CompletionItemKind.Field,
            insertText: c,
            range,
          })),
        }
      }

      return {
        suggestions: [
          ...Object.keys(tables).map((t) => ({
            label: t,
            kind: monaco.languages.CompletionItemKind.Struct,
            insertText: t,
            detail: `${(tables[t] ?? []).length} columns`,
            range,
          })),
          // Unqualified column names, de-duplicated across tables: that is what
          // people actually type in a single-table query.
          ...[...new Set(Object.values(tables).flat())].map((c) => ({
            label: c,
            kind: monaco.languages.CompletionItemKind.Field,
            insertText: c,
            range,
          })),
        ],
      }
    },
  })
}
