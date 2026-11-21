"use client"

import dynamic from "next/dynamic"
import { MONACO_VS } from "@/lib/vendored"
import { Spinner } from "@/components/loading"
import { cn } from "@/lib/utils"

// Monaco's diff editor, loaded the way `CodeEditor` loads the editor: only
// when a diff is drawn, and from this dashboard's own origin rather than a
// CDN (see code-editor.tsx for why that matters on a closed network).
const DiffEditor = dynamic(
  () =>
    import("@monaco-editor/react").then(async (mod) => {
      mod.loader.config({ paths: { vs: `${window.location.origin}${MONACO_VS}` } })
      return mod.DiffEditor
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
 * Two contents of one file side by side, the older on the left, read-only.
 * Below the width a side-by-side diff needs, Monaco folds it into one column
 * of its own accord.
 */
export function VersionDiff({
  original,
  modified,
  language,
  className,
}: {
  original: string
  modified: string
  language?: string
  className?: string
}) {
  return (
    <div data-version-diff className={cn("monaco-host min-h-0 flex-1", className)}>
      <DiffEditor
        height="100%"
        theme="vs-dark"
        language={language}
        original={original}
        modified={modified}
        options={{
          readOnly: true,
          originalEditable: false,
          renderSideBySide: true,
          useInlineViewWhenSpaceIsLimited: true,
          renderSideBySideInlineBreakpoint: 520,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          automaticLayout: true,
          fontSize: 13,
          lineHeight: 20,
          renderLineHighlight: "none",
          renderOverviewRuler: false,
          padding: { top: 12, bottom: 12 },
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
          hideUnchangedRegions: { enabled: true, contextLineCount: 3, minimumLineCount: 6 },
        }}
      />
    </div>
  )
}
