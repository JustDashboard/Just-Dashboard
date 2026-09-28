"use client"

import { useMemo } from "react"
import { FileText, Warning } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import type { ProxyConfigEntry, ProxyConfigFiles } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { SearchInput, Toolbar } from "@/components/page"
import { ChoiceRow, GroupRule } from "@/components/flow"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ProxyGrid } from "@/components/proxy/route-path"
import { openableFile } from "@/components/proxy/engine-lifecycle"
import {
  editorPath,
  entryNote,
  entryTags,
  FILTER_LABEL,
  filterCounts,
  groupByFolder,
  readState,
  relativePath,
  visibleFiles,
  type FileFilter,
} from "@/components/proxy/config-tree"

const FILTERS: FileFilter[] = ["all", "read", "unread", "managed", "hand"]

/**
 * The nginx directory as a tree: a run of files per folder, the directory's
 * own first, each saying whether nginx reads it and through which include,
 * whether the dashboard wrote it, and opening it in the config editor. A
 * password file is named and never opened; a link opens the file it points
 * at, and one that points outside the directory or at nothing opens nothing.
 */
export function ConfigFilesView({
  tree,
  admin,
  onOpen,
}: {
  tree: ProxyConfigFiles
  admin: boolean
  onOpen: (path: string, line?: number) => void
}) {
  const [query, setQuery] = useSessionState("proxy.config.query", "")
  const [filter, setFilter] = useSessionState<FileFilter>("proxy.config.filter", "all")
  const counts = useMemo(() => filterCounts(tree.files), [tree.files])
  // A remembered filter with nothing under it any more shows every file
  // rather than an empty list with no chip lit to explain it.
  const active = filter !== "all" && counts[filter] === 0 ? "all" : filter
  const groups = useMemo(
    () => groupByFolder(visibleFiles(tree.files, tree.root, active, query), tree.root),
    [tree.files, tree.root, active, query],
  )
  const problem = tree.problem
  const problemFile = problem?.file

  return (
    <div className="min-w-0 space-y-5">
      {problem && (
        <Notice tone="warning" icon={Warning} title="Not every include can be followed">
          <div className="space-y-2">
            <p>
              {problemFile && (
                <span className="font-mono break-all">
                  {relativePath(problemFile, tree.root)}
                  {problem.line ? `:${problem.line}` : ""}
                </span>
              )}
              {problemFile && ": "}
              {problem.message}.
              {problemFile &&
                !tree.includesKnown &&
                " Files only it would include show as not known until it can be read."}
            </p>
            {problemFile && openableFile(problemFile, [tree.root]) && (
              <Button size="xs" variant="outline" onClick={() => onOpen(problemFile, problem.line)}>
                {problem.line ? `Open at line ${problem.line}` : "Open file"}
              </Button>
            )}
          </div>
        </Notice>
      )}

      {tree.files.length === 0 ? (
        <EmptyState
          icon={FileText}
          title="No configuration files"
          description={`Nothing is in ${tree.root} to read.`}
        />
      ) : (
        <>
          <Toolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="File or folder"
              aria-label="Search files"
            />
            <ChipStrip>
              {FILTERS.filter((key) => key === "all" || counts[key] > 0).map((key) => (
                <FilterChip key={key} selected={active === key} onClick={() => setFilter(key)}>
                  {FILTER_LABEL[key]}
                  <ChipCount>{counts[key]}</ChipCount>
                </FilterChip>
              ))}
            </ChipStrip>
          </Toolbar>

          {groups.length === 0 ? (
            <EmptyNote>No files match.</EmptyNote>
          ) : (
            groups.map((group) => {
              const where = group.folder ? `${group.folder}/` : tree.root
              return (
                <section key={group.folder} className="min-w-0 space-y-2">
                  <GroupRule
                    label={group.folder || "nginx directory"}
                    count={group.entries.length}
                    detail={group.folder ? undefined : tree.root}
                  />
                  <ProxyGrid className="gap-2" aria-label={`Files in ${where}`}>
                    {group.entries.map((entry) => (
                      <FileRow
                        key={entry.path}
                        entry={entry}
                        root={tree.root}
                        includesKnown={tree.includesKnown}
                        admin={admin}
                        onOpen={onOpen}
                      />
                    ))}
                  </ProxyGrid>
                </section>
              )
            })
          )}
          {tree.truncated && (
            <p className="text-hint text-muted-foreground">
              Showing the first {tree.files.length.toLocaleString()} files; the directory holds
              more.
            </p>
          )}
        </>
      )}
    </div>
  )
}

function FileRow({
  entry,
  root,
  includesKnown,
  admin,
  onOpen,
}: {
  entry: ProxyConfigEntry
  root: string
  includesKnown: boolean
  admin: boolean
  onOpen: (path: string) => void
}) {
  const open = editorPath(entry)
  // A password file is read by nginx's workers on a request, never through
  // an include, so "not read" would be untrue of it.
  const state = entry.protected ? undefined : readState(entry, includesKnown)
  return (
    <ChoiceRow
      disabled={!open}
      onSelect={open ? () => onOpen(open) : undefined}
      verb={`${admin ? "Edit" : "View"} ${relativePath(entry.path, root)}`}
      className="min-h-0 gap-3 px-3 py-2.5"
      title={<span className="font-mono">{entry.path.split("/").pop()}</span>}
      description={entryNote(entry, root, bytes, (iso) => relativeTime(iso))}
      trailing={
        <>
          {entryTags(entry).map((tag) => (
            <Tag key={tag.label} tone={tag.tone}>
              {tag.label}
            </Tag>
          ))}
          {state && <Status tone={state.tone} label={state.label} />}
        </>
      }
    />
  )
}
