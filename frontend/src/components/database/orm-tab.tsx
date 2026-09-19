"use client"

import { useState } from "react"
import { useViewState } from "@/lib/view-state"
import { Copy, Database, Download } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { DbConnection, OrmTarget, OrmTargetInfo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { CodeEditor } from "@/components/code-editor"
import { Button } from "@/components/ui/button"
import { Pane, Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { FilterChip } from "@/components/tabs"
import { EmptyState, Notice, Spinner } from "@/components/state"
import { copyText } from "@/lib/clipboard"

/**
 * Code generation, done the way a server panel honestly can: introspect the
 * live database and produce the file a developer would otherwise get from
 * `prisma db pull` or `drizzle-kit pull`, with no Node toolchain on the box.
 * The result is a reviewed starting point, which the generated header and the
 * footer here both say plainly.
 *
 * The list of generators comes from the server rather than being written out
 * here. It was two hardcoded buttons, and adding a generator meant editing this
 * file to match — which is exactly the second list that drifts.
 */
export function OrmTab({ conn, schema }: { conn: DbConnection; schema: string }) {
  const [target, setTarget] = useViewState<OrmTarget>("db.orm.target", "prisma")
  const [output, setOutput] = useState<{ schema: string; filename: string } | null>(null)
  const [busy, setBusy] = useState(false)

  const targets = usePoll(
    (signal) =>
      get<{ targets: OrmTargetInfo[] }>("/databases/orm/targets", undefined, signal).then(
        (r) => r.targets,
      ),
    0,
  )

  if (conn.driver === "mongodb") {
    return (
      <Notice tone="default" title="Not available for MongoDB">
        Schema generation introspects a relational catalogue. Use your driver&apos;s native tooling
        for a document database.
      </Notice>
    )
  }

  const generate = async (t: OrmTarget) => {
    setTarget(t)
    setBusy(true)
    try {
      const res = await post<{ schema: string; filename: string }>(`/databases/${conn.id}/orm`, {
        target: t,
        schema,
      })
      setOutput(res)
    } catch (err) {
      notify.error("Generation failed", err)
    } finally {
      setBusy(false)
    }
  }

  const download = () => {
    if (!output) return
    const blob = new Blob([output.schema], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = output.filename
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Generate from this schema"
        actions={
          <div className="flex flex-wrap items-center gap-1">
            {/* The chosen generator is a selection, so it takes the neutral
                fill every selection does — not the brand face, which is the
                mark of the one command on a surface. */}
            {targets.data?.map((t) => (
              <FilterChip
                key={t.id}
                selected={output !== null && target === t.id}
                onClick={() => generate(t.id)}
                disabled={busy}
                title={t.description}
              >
                {busy && target === t.id ? <Spinner className="size-3" /> : null}
                {t.label}
              </FilterChip>
            ))}
          </div>
        }
      />
      <PanelBody flush>
        {!output && !busy && (
          <EmptyState
            icon={Database}
            title="Nothing generated yet"
            description="Pick a generator above to introspect the current schema. Prisma and Drizzle produce an ORM schema; TypeScript gives you plain interfaces with no runtime dependency; Zod gives you validators you can parse API input with."
          />
        )}
        {busy && !output && (
          <div className="flex items-center justify-center gap-2 p-10 text-body text-muted-foreground">
            <Spinner /> Introspecting {schema || "database"}…
          </div>
        )}
        {output && (
          <Pane>
            <CodeEditor
              className="h-[calc(100svh-26rem)] min-h-64"
              language={output.filename.endsWith(".prisma") ? "prisma" : "typescript"}
              value={output.schema}
              readOnly
            />
          </Pane>
        )}
      </PanelBody>
      {output && (
        <PanelFooter>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void copyText(output.schema, "Copied schema")}
          >
            <Copy className="size-3.5" />
            Copy
          </Button>
          <Button size="sm" variant="outline" onClick={download}>
            <Download className="size-3.5" />
            Download {output.filename}
          </Button>
          <span className="text-xs text-muted-foreground">
            A reviewed starting point — verify types and relations before committing.
          </span>
        </PanelFooter>
      )}
    </Panel>
  )
}
