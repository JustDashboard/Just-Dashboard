"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { ProxyLint } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FindingList, type Finding } from "@/components/finding-list"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { relativePath } from "@/components/proxy/config-tree"

const LEVEL_ORDER: Record<Finding["level"], number> = { critical: 0, warning: 1, notice: 2 }

/**
 * What the configuration nginx loads does that nginx accepts but an operator
 * rarely means: headers a block drops, an alias that climbs out, a status page
 * open to anyone. Each finding opens the config editor at its line. It reads
 * the same `nginx -T` as What nginx loads, so it is read on request, not
 * polled.
 */
export function ConfigLintView({
  root,
  onOpen,
}: {
  root: string
  onOpen: (path: string, line?: number) => void
}) {
  const lint = usePoll((signal) => get<ProxyLint>("/proxy/lint", undefined, signal), 0)
  const findings = useMemo<Finding[]>(
    () =>
      (lint.data?.findings ?? [])
        .map((f) => ({
          id: f.id,
          level: f.level,
          title: f.title,
          detail: f.detail,
          meta: (
            <span className="font-mono text-hint text-muted-foreground">
              {relativePath(f.file, root)}:{f.line}
            </span>
          ),
          action: { label: "Open at line", onClick: () => onOpen(f.file, f.line) },
        }))
        .sort((a, b) => LEVEL_ORDER[a.level] - LEVEL_ORDER[b.level]),
    [lint.data, root, onOpen],
  )

  if (lint.error) return <ErrorState error={lint.error} onRetry={lint.refresh} />
  return (
    <Panel plain>
      <PanelHeader
        title={lint.data ? plural(findings.length, "finding") : "Findings"}
        actions={
          <Button size="sm" variant="outline" onClick={lint.refresh} pending={lint.loading}>
            Read again
          </Button>
        }
      />
      <PanelBody>
        {!lint.data ? (
          <LoadingRows rows={4} />
        ) : (
          <FindingList
            findings={findings}
            emptyLabel="Nothing the linter checks for is in what nginx loads"
          />
        )}
      </PanelBody>
    </Panel>
  )
}
