"use client"

import { useMemo, useState } from "react"
import { ArrowRight, Warning } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry, GitStatus, StackDetail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useNow } from "@/components/deploy/vocabulary"
import { FileEditorSheet } from "@/components/files/file-editor"
import { FileIcon } from "@/components/files/file-icon"
import { FileBrowser } from "@/components/files/inline-browser"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ago } from "@/components/procs/units"
import { EmptyNote, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ServiceLabel } from "@/components/docker/stack-diff"
import { serviceLane } from "@/components/docker/stack-service-readings"
import { fileRoles, type FileRole } from "@/components/docker/stack-views"

/** One path compose reads, with everything that reads it and what is there. */
type Row = {
  /** Inside the stack's directory; "" is the directory itself. */
  path: string
  abs: string
  kind: "compose" | "variables" | "used"
  uses: FileRole[]
  entry?: FileEntry
  missing: boolean
}

/** What a path that is not there costs, by what reads it. */
function missingWords(row: Row) {
  if (row.uses.some((u) => u.role === "build")) return "Missing: compose cannot build from it"
  if (row.uses.some((u) => u.role === "env"))
    return "Missing: compose refuses to start what reads it"
  return "Missing: Docker creates it empty, owned by root"
}

/**
 * The stack's directory, read as what compose takes from it.
 *
 * The directory was a listing that stopped a third of the way down the
 * screen. A listing says what is there; what an operator came for is which
 * of it matters — the folder a service is built from, the config mounted
 * into the web server, the env files a service reads, the file that defines
 * the stack — and whether any of it is missing or has changed since the last
 * commit. That is read out of the compose file and laid over the directory
 * as a table, each path with the services that read it in their lanes, and
 * the same words follow the names into the browser under it, which now
 * takes the height the tab has.
 */
export function StackFiles({
  stack,
  productOf,
  onOpenCompose,
}: {
  stack: StackDetail
  productOf: (service: string) => string | undefined
  onOpenCompose: () => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const now = useNow(60_000)
  const root = stack.workingDir.replace(/\/+$/, "")
  const [goTo, setGoTo] = useState<{ path: string; key: number }>()
  const [editing, setEditing] = useState<string | null>(null)

  const config = usePoll<{ content: string }>(
    (signal) =>
      get<{ content: string }>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
        undefined,
        signal,
      ),
    0,
    [stack.name],
    { enabled: Boolean(stack.configPath) },
  )
  const composeName = stack.configPath?.startsWith(`${root}/`)
    ? stack.configPath.slice(root.length + 1)
    : undefined

  // Every path compose reads, stat'ed: a missing env file or build context
  // is the reason a deploy fails, and the listing cannot say what is absent.
  const rows = usePoll<Row[]>(
    async (signal) => {
      const uses = fileRoles(config.data?.content ?? "")
      const paths = [
        ...(composeName ? [composeName] : []),
        ".env",
        ...new Set(uses.map((u) => u.path)),
      ].filter((path, i, all) => all.indexOf(path) === i)
      const stats = await Promise.allSettled(
        paths.map((path) =>
          get<FileEntry>("/files/stat", { path: path ? `${root}/${path}` : root }, signal),
        ),
      )
      return paths.flatMap((path, i): Row[] => {
        const stat = stats[i]
        const entry = stat.status === "fulfilled" ? stat.value : undefined
        const missing =
          stat.status === "rejected" &&
          stat.reason instanceof ApiError &&
          (stat.reason.status === 404 || stat.reason.code === "not_found")
        const kind = path === composeName ? "compose" : path === ".env" ? "variables" : "used"
        // The root `.env` is compose's own when nothing names it; absent, it is not missing.
        if (kind === "variables" && !entry && !uses.some((u) => u.path === ".env")) return []
        return [
          {
            path,
            abs: path ? `${root}/${path}` : root,
            kind,
            uses: uses.filter((u) => u.path === path),
            entry,
            missing,
          },
        ]
      })
    },
    0,
    [root, composeName, config.data?.content],
    { enabled: !stack.configPath || config.data !== undefined || config.error !== undefined },
  )

  const git = usePoll<GitStatus>(
    (signal) => get<GitStatus>("/git/status", { path: stack.git?.path }, signal),
    30_000,
    [stack.git?.path],
    { enabled: Boolean(stack.git) },
  )
  const changes = useMemo(() => gitMarks(git.data, root), [git.data, root])

  const notes = useMemo(() => {
    const out: Record<string, React.ReactNode> = {}
    for (const row of rows.data ?? []) {
      out[row.abs] = <UseNote row={row} />
    }
    for (const [path, label] of changes) {
      out[path] = (
        <>
          {out[path]}
          <GitTag label={label} />
        </>
      )
    }
    return out
  }, [rows.data, changes])

  const open = (row: Row) => {
    if (row.kind === "compose") return onOpenCompose()
    if (row.missing) return
    if (row.entry?.isDir || row.path === "")
      setGoTo((prev) => ({ path: row.abs, key: (prev?.key ?? 0) + 1 }))
    else setEditing(row.abs)
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 animate-rise flex-col gap-6 pb-4">
      <Panel aria-label="What compose reads here" className="shrink-0">
        <PanelHeader
          title="What compose reads here"
          actions={<span className="font-mono text-hint text-muted-foreground">{root}</span>}
        />
        <PanelBody flush>
          {!rows.data ? (
            <LoadingRows rows={3} className="p-4" />
          ) : rows.data.length === 0 ? (
            <EmptyNote>The compose file names nothing in this directory.</EmptyNote>
          ) : wide ? (
            <Table className="table-fixed">
              <TableHeader>
                <TableRow>
                  <TableHead className="w-64 pl-5">Path</TableHead>
                  <TableHead>What reads it</TableHead>
                  <TableHead className="w-32">Changes</TableHead>
                  <TableHead className="w-20 text-right">Size</TableHead>
                  <TableHead className="w-28 pr-5">Modified</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.data.map((row) => (
                  <TableRow
                    key={row.abs}
                    data-path={row.path}
                    onActivate={row.missing ? undefined : () => open(row)}
                  >
                    <TableCell className="py-2 pl-5">
                      <PathName row={row} onOpen={() => open(row)} />
                    </TableCell>
                    <TableCell className="py-2 whitespace-normal">
                      <Uses row={row} productOf={productOf} />
                    </TableCell>
                    <TableCell className="py-2">
                      <GitCell label={changes.get(row.abs)} />
                    </TableCell>
                    <TableCell className="numeric py-2 text-right font-mono text-hint text-muted-foreground">
                      {row.entry && !row.entry.isDir ? bytes(row.entry.size) : "—"}
                    </TableCell>
                    <TableCell
                      className="py-2 pr-5 text-hint whitespace-nowrap text-muted-foreground"
                      title={row.entry ? timestamp(row.entry.modified) : undefined}
                    >
                      {row.entry ? ago(Date.parse(row.entry.modified) / 1000, now) : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <ul className="divide-y divide-hairline">
              {rows.data.map((row) => (
                <li key={row.abs} data-path={row.path} className="flex flex-col gap-1.5 px-5 py-3">
                  <span className="flex min-w-0 items-center justify-between gap-3">
                    <PathName row={row} onOpen={() => open(row)} />
                    <GitCell label={changes.get(row.abs)} />
                  </span>
                  <Uses row={row} productOf={productOf} />
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <FileBrowser
        root={root}
        label={stack.name}
        emptyNote="This stack's directory is empty."
        fill
        className="min-h-[24rem]"
        goTo={goTo}
        notes={notes}
      />

      <FileEditorSheet path={editing} onOpenChange={(o) => !o && setEditing(null)} root={root} />
    </div>
  )
}

/** Each changed path, and every folder above it inside the stack, by what git calls the change. */
function gitMarks(status: GitStatus | undefined, root: string) {
  const out = new Map<string, string>()
  if (!status) return out
  const repo = status.repo.path.replace(/\/+$/, "")
  for (const file of status.files) {
    const abs = `${repo}/${file.path.replace(/\/+$/, "")}`
    if (!abs.startsWith(`${root}/`)) continue
    out.set(abs, file.label)
    for (let dir = abs.slice(0, abs.lastIndexOf("/")); dir.length > root.length;) {
      if (!out.has(dir)) out.set(dir, "changes inside")
      dir = dir.slice(0, dir.lastIndexOf("/"))
    }
  }
  return out
}

function PathName({ row, onOpen }: { row: Row; onOpen: () => void }) {
  const name = row.path || "."
  const isDir = row.entry?.isDir ?? (row.uses.some((u) => u.role === "build") || row.path === "")
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <FileIcon
        entry={{ name: name.split("/").pop() ?? name, isDir, isSymlink: false }}
        className={cn("size-5 shrink-0", row.missing && "opacity-40 grayscale")}
      />
      <button
        type="button"
        onClick={onOpen}
        disabled={row.missing}
        title={row.abs}
        className={cn(
          "min-w-0 truncate rounded-sm text-left font-mono text-body focus-ring enabled:hover:underline",
          row.missing && "text-muted-foreground line-through decoration-1",
        )}
      >
        {name}
        {isDir && name !== "." && "/"}
      </button>
      {row.kind === "compose" && (
        <ArrowRight aria-hidden className="size-3 text-muted-foreground" />
      )}
    </span>
  )
}

const ROLE_WORDS: Record<FileRole["role"], string> = {
  build: "built from it",
  mount: "mounted",
  env: "reads its variables",
}

/** Who reads the path and how, each service in its lane — or why it matters without one. */
function Uses({
  row,
  productOf,
}: {
  row: Row
  productOf: (service: string) => string | undefined
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      {row.missing && (
        <span
          className={cn(
            "flex items-center gap-1.5 text-hint font-medium",
            row.uses.some((u) => u.role !== "mount") ? "text-destructive" : "text-warning",
          )}
        >
          <Warning aria-hidden className="size-3 shrink-0" />
          {missingWords(row)}
        </span>
      )}
      {row.kind === "compose" && (
        <span className="text-hint text-muted-foreground">
          The stack itself: every service is defined here
        </span>
      )}
      {row.kind === "variables" && (
        <span className="text-hint text-muted-foreground">
          Compose fills the file&apos;s <span className="font-mono">${"{…}"}</span> from it
        </span>
      )}
      {row.uses.length > 0 && (
        <ul className="flex min-w-0 flex-wrap gap-x-4 gap-y-1">
          {row.uses.map((use, i) => (
            <li key={i} className="flex min-w-0 items-center gap-1.5 text-hint">
              <ServiceLabel name={use.service} product={productOf(use.service)} />
              <span className="text-muted-foreground">
                {ROLE_WORDS[use.role]}
                {use.target && (
                  <>
                    {" at "}
                    <span className="font-mono text-foreground">{use.target}</span>
                  </>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** The short form that follows a name into the browser below, each service in its lane's hue. */
function UseNote({ row }: { row: Row }) {
  if (row.kind === "compose") return <Tag>the stack</Tag>
  const services = [...new Set(row.uses.map((u) => u.service))]
  if (services.length === 0) return null
  const role = row.uses[0].role
  return (
    <span className="shrink-0 text-hint text-muted-foreground">
      {role === "build" ? "built into " : role === "env" ? "read by " : "mounted into "}
      {services.map((service, i) => (
        <span key={service}>
          {i > 0 && ", "}
          <span className="font-medium" style={{ color: serviceLane(service) }}>
            {service}
          </span>
        </span>
      ))}
    </span>
  )
}

function GitTag({ label }: { label: string }) {
  return <Tag tone={label === "changes inside" ? "default" : "warning"}>{label}</Tag>
}

function GitCell({ label }: { label?: string }) {
  if (!label) return <span className="text-hint text-muted-foreground">—</span>
  return <GitTag label={label} />
}
