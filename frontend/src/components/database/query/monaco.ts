"use client"

import { loader } from "@monaco-editor/react"
import {
  quotedReach,
  subjectAt,
  subjectNote,
  suggest,
  type SchemaModel,
  type SuggestionKind,
  type TableColumn,
  type Vocabulary,
} from "@/components/database/query/completion"
import type { Dialect } from "@/components/database/query/dialect"

/**
 * The SQL editor's own hold on Monaco.
 *
 * The shared `CodeEditor` draws the editor and keeps it to itself: it hands
 * out no instance, and its schema completion reads the schema an editor was
 * first rendered with — a new editor on another connection went on offering
 * the last connection's tables. What a query editor needs beyond typing — the
 * statement under the cursor, a key that runs it, names completed from *this*
 * connection — is done here, from the same Monaco the shared editor loads.
 *
 * Completion is registered once per language and looks the schema up by the
 * document being typed in. An editor writes its document's schema into the
 * table when it has one and takes it out when it goes, so nothing a closed
 * editor knew is ever offered in another.
 */

export type Monaco = typeof import("monaco-editor")
export type MonacoEditor = import("monaco-editor").editor.IStandaloneCodeEditor

let ready: Promise<Monaco> | undefined

/** Monaco, from this origin: the same loader and the same files the shared editor uses. */
export function loadMonaco(): Promise<Monaco> {
  if (!ready) {
    loader.config({ paths: { vs: `${window.location.origin}/monaco/vs` } })
    ready = loader.init() as Promise<Monaco>
  }
  return ready
}

/** The editor drawn inside `host`, now or as soon as it exists. Returns how to stop waiting. */
export function whenEditor(
  monaco: Monaco,
  host: HTMLElement,
  found: (editor: MonacoEditor) => void,
): () => void {
  let done = false
  const look = () => {
    if (done) return
    const editor = monaco.editor.getEditors().find((candidate) => {
      const node = candidate.getDomNode()
      return node !== null && host.contains(node) && candidate.getModel() !== null
    })
    if (!editor) return
    done = true
    subscription.dispose()
    found(editor as MonacoEditor)
  }
  // The event fires inside the editor's constructor, before it has its
  // document: looked at again once the constructor has returned.
  const subscription = monaco.editor.onDidCreateEditor(() => setTimeout(look, 0))
  look()
  return () => {
    done = true
    subscription.dispose()
  }
}

type TextModel = import("monaco-editor").editor.ITextModel
type ViewState = import("monaco-editor").editor.ICodeEditorViewState

/**
 * The documents the editor's tabs are written in, kept while their tabs are.
 *
 * The shared editor makes a new document each time it is drawn, and a tab is
 * drawn anew each time it comes to the front — so a reader who looked at
 * another tab and came back could not undo their last edit, and found the
 * cursor at the top. A tab's document is kept here instead, with its history
 * and where the reader was in it, and handed back to the editor that draws
 * the tab next. It is let go when the tab is closed for good.
 */
const documents = new Map<string, { model: TextModel; view: ViewState | null }>()

/**
 * Gives an editor the document kept under `key`, or keeps the one it was
 * drawn with. A kept document whose text is not the tab's text is some other
 * statement's — a tab id used again after the session's tabs were cleared —
 * and is let go rather than shown. Returns the document the editor now holds.
 */
export function adoptDocument(editor: MonacoEditor, key: string): TextModel | null {
  const born = editor.getModel()
  if (!born) return null
  const kept = documents.get(key)
  if (kept && kept.model === born) return born
  if (kept && !kept.model.isDisposed() && kept.model.getValue() === born.getValue()) {
    editor.setModel(kept.model)
    born.dispose()
    if (kept.view) editor.restoreViewState(kept.view)
    return kept.model
  }
  if (kept && !kept.model.isDisposed()) kept.model.dispose()
  documents.set(key, { model: born, view: null })
  return born
}

/**
 * Takes the kept document out of an editor that is going away, with where
 * the reader was in it, so the editor's own disposal does not take the
 * document with it.
 */
export function releaseDocument(editor: MonacoEditor, key: string) {
  const kept = documents.get(key)
  if (!kept || editor.getModel() !== kept.model) return
  kept.view = editor.saveViewState()
  editor.setModel(null)
}

/** Lets a closed tab's document go. */
export function forgetDocument(key: string) {
  const kept = documents.get(key)
  if (!kept) return
  documents.delete(key)
  if (!kept.model.isDisposed()) kept.model.dispose()
}

export type { TableColumn }

export interface CompletionSource {
  model: SchemaModel
  dialect: Dialect
  vocabulary: Vocabulary
  /** A table's columns with their types, read when a note about it is asked for. */
  describe?: (schema: string, table: string) => Promise<TableColumn[]>
}

/** The schema each open document completes from, by the document's address. */
const sources = new Map<string, () => CompletionSource>()
const provided = new Set<string>()

/** Ties a document to the schema it completes from. Returns how to untie it. */
export function completeFrom(uri: string, source: () => CompletionSource): () => void {
  sources.set(uri, source)
  return () => {
    if (sources.get(uri) === source) sources.delete(uri)
  }
}

/** Registers the completion for a language, once however many editors ask. */
export function provideCompletion(monaco: Monaco, language: string) {
  if (provided.has(language)) return
  provided.add(language)
  const kinds: Record<SuggestionKind, number> = {
    column: monaco.languages.CompletionItemKind.Field,
    table: monaco.languages.CompletionItemKind.Struct,
    view: monaco.languages.CompletionItemKind.Interface,
    schema: monaco.languages.CompletionItemKind.Module,
    alias: monaco.languages.CompletionItemKind.Variable,
    join: monaco.languages.CompletionItemKind.Reference,
    function: monaco.languages.CompletionItemKind.Function,
    keyword: monaco.languages.CompletionItemKind.Keyword,
  }
  monaco.languages.registerCompletionItemProvider(language, {
    // A dot asks for what the name before it holds. A space asks only where
    // what comes next is known to be a table or a join — after FROM, JOIN and
    // ON — so the list does not open after every word.
    triggerCharacters: [".", " "],
    provideCompletionItems(model, position, context) {
      const source = sources.get(model.uri.toString())?.()
      if (!source) return { suggestions: [] }
      if (context.triggerCharacter === " ") {
        const before = model
          .getLineContent(position.lineNumber)
          .slice(0, position.column - 1)
          .trimEnd()
        if (!/(?:^|[\s(])(?:from|join|on)$/i.test(before)) return { suggestions: [] }
      }
      const word = model.getWordUntilPosition(position)
      const line = model.getLineContent(position.lineNumber)
      // A name being typed inside its quote is replaced quote and all, so the
      // quoted form that is written does not open a second one.
      const { start, end } = quotedReach(line, word.startColumn, word.endColumn, position.column, [
        source.dialect.quote,
        ...source.dialect.alsoQuotes,
      ])
      // The opening mark, when the name is being typed inside one.
      const mark = start < word.startColumn ? line[start - 1] : ""
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: start,
        endColumn: end,
      }
      const offered = suggest(
        source.model,
        source.dialect,
        source.vocabulary,
        model.getValue(),
        model.getOffsetAt(position),
      )
      return {
        suggestions: offered.map((item) => ({
          label: item.label,
          kind: kinds[item.kind],
          detail: item.detail,
          insertText: item.insert,
          filterText: `${mark}${item.label}`,
          insertTextRules:
            item.kind === "function"
              ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
              : undefined,
          // Rank first, then the name: what belongs here comes before what is merely legal.
          sortText: `${item.rank}${item.label.toLowerCase()}`,
          range,
        })),
      }
    },
  })
}

const hovering = new Set<string>()

/**
 * Registers the note shown where the pointer rests on a name, once per
 * language: a table with its columns and their types, a column with its
 * type and the table it belongs to. The types are read when asked for; a
 * note that cannot read them still names the columns.
 */
export function provideHover(monaco: Monaco, language: string) {
  if (hovering.has(language)) return
  hovering.add(language)
  monaco.languages.registerHoverProvider(language, {
    async provideHover(model, position) {
      const source = sources.get(model.uri.toString())?.()
      if (!source) return null
      const subject = subjectAt(
        source.model,
        source.dialect,
        model.getValue(),
        model.getOffsetAt(position),
      )
      if (!subject) return null
      const { table } = subject
      const columns = await (source.describe?.(table.schema, table.name) ?? Promise.resolve([]))
        .then((read) => (read.length > 0 ? read : undefined))
        .catch(() => undefined)
      return { contents: [{ value: subjectNote(subject, columns) }] }
    },
  })
}
