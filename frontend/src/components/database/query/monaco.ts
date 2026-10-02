"use client"

import { loader } from "@monaco-editor/react"
import {
  suggest,
  type SchemaModel,
  type SuggestionKind,
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

export interface CompletionSource {
  model: SchemaModel
  dialect: Dialect
  vocabulary: Vocabulary
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
    function: monaco.languages.CompletionItemKind.Function,
    keyword: monaco.languages.CompletionItemKind.Keyword,
  }
  monaco.languages.registerCompletionItemProvider(language, {
    triggerCharacters: ["."],
    provideCompletionItems(model, position) {
      const source = sources.get(model.uri.toString())?.()
      if (!source) return { suggestions: [] }
      const word = model.getWordUntilPosition(position)
      const line = model.getLineContent(position.lineNumber)
      // A name being typed inside its quote is replaced quote and all, so the
      // quoted form that is written does not open a second one.
      const before = line[word.startColumn - 2]
      const opens = [source.dialect.quote, ...source.dialect.alsoQuotes].some(
        ([open]) => open === before,
      )
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: opens ? word.startColumn - 1 : word.startColumn,
        endColumn: word.endColumn,
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
          filterText: opens ? `${before}${item.label}` : item.label,
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
