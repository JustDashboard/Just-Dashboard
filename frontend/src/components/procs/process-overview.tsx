"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Copy,
  Cpu,
  FolderOpen,
  Globe,
  NetworkDevice,
  Warning,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, percent, plural, timestamp } from "@/lib/format"
import type { ProcessInspect, ProcessLink, ProcessRow, ProcessTree } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList, Section } from "@/components/page"
import { Well } from "@/components/panel"
import {
  ProductGlyph,
  hasProductLogo,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { RankList, SplitBar } from "@/components/rank-list"
import { RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Counter, ProcessLinkRow, ProcessReadings } from "@/components/procs/process-detail"
import {
  idWords,
  memoryParts,
  namespaceWords,
  oomWords,
  processWarnings,
  staleCode,
} from "@/components/procs/process-facts"
import {
  cpuTone,
  managerHref,
  managerName,
  ownerName,
  processStateTone,
  reach,
  uncontrollable,
} from "@/components/procs/shared"
import { WiringPicture, type PictureNode } from "@/components/procs/wiring-picture"

/** Kinds of child drawn in the picture before the rest fold into a count. */
const CHILDREN_DRAWN = 4

/** Where the picture's folded children are listed, for a press on them to scroll to. */
const CHILDREN_LIST = "process-children"

/** A process's children by program, busiest program first, as `Tree` already orders them. */
function childGroups(children: ProcessLink[]): ProcessLink[][] {
  const groups = new Map<string, ProcessLink[]>()
  for (const link of children) {
    const group = groups.get(link.name)
    if (group) group.push(link)
    else groups.set(link.name, [link])
  }
  return [...groups.values()]
}

/** Below this a child is idle and its line is still. */
const BUSY = 1

export type OverviewProps = {
  process: ProcessRow
  gone?: "exited" | "replaced"
  inspect?: ProcessInspect
  tree?: ProcessTree
  memTotal: number
  onOpen: (pid: number) => void
  onTab: (tab: string) => void
}

/**
 * The process's Overview, one scroll in the order it is asked about: what is
 * wrong, what it is using now, where it sits — what supervises it, what
 * started it, what it started and what it listens on, as one wired picture —
 * the command as it was run, who it runs as and how it is scheduled, and
 * where its memory goes.
 */
export function ProcessOverview(props: OverviewProps) {
  const { process: row, gone, inspect, tree, memTotal, onOpen, onTab } = props
  const children = tree?.children ?? []
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <ProcessNotices {...props} />
      <ProcessReadings process={row} memTotal={memTotal} />
      <ProcessPicture {...props} />
      {(childGroups(children).length > CHILDREN_DRAWN ||
        childGroups(children).some((group) => group.length > 1)) && (
        <div id={CHILDREN_LIST} className="scroll-mt-4">
          <Section
            title={
              <>
                Started by it
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {children.length}
                </span>
              </>
            }
          >
            <RowList>
              {children.map((link) => (
                <ProcessLinkRow key={link.pid} link={link} onClick={() => onOpen(link.pid)} />
              ))}
            </RowList>
          </Section>
        </div>
      )}
      <CommandBlock process={row} inspect={inspect} />
      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <WhoItRunsAs process={row} inspect={inspect} onTab={onTab} />
        <HowItIsScheduled process={row} inspect={inspect} gone={gone} />
      </div>
      {uncontrollable(row) !== "kernel" && <MemoryBlock process={row} inspect={inspect} />}
    </div>
  )
}

function ProcessNotices({ process: row, gone, inspect, onOpen, onTab }: OverviewProps) {
  const kind = uncontrollable(row)
  const stale = staleCode(inspect)
  const warnings = gone ? [] : processWarnings(row, inspect)
  const owner = managerHref(row)
  const restartWith = owner ? ownerName(row) : row.name
  const notices: React.ReactNode[] = []
  if (gone) {
    notices.push(
      <Notice
        key="gone"
        tone="warning"
        title={
          gone === "replaced"
            ? "This PID now belongs to another process"
            : "This process has exited"
        }
      >
        {gone === "replaced"
          ? `${row.name} exited and PID ${row.pid} was given to a new process. What is below is ${row.name}'s last reading.`
          : "What is below is its last reading. A supervisor that restarts it starts it under a new PID."}
      </Notice>,
    )
  }
  if (!gone && kind === "zombie") {
    notices.push(
      <Notice key="zombie" tone="danger" title="Exited, and not yet reaped">
        <p>
          What is left is its exit status, which only its parent can collect; it holds no memory and
          takes no signal. One is harmless. Many mean the parent is not reaping its children, and
          restarting or ending the parent clears them.
        </p>
        {row.ppid > 0 && (
          <Button size="xs" variant="outline" className="mt-2" onClick={() => onOpen(row.ppid)}>
            Open its parent, PID {row.ppid}
          </Button>
        )}
      </Notice>,
    )
  }
  if (!gone && kind === "kernel") {
    notices.push(
      <Notice key="kernel" title="A kernel thread">
        The kernel runs it for itself. It has no command line, memory or files of its own, and does
        not take signals; its priority can still be set.
      </Notice>,
    )
  }
  if (!gone && row.state === "stopped") {
    notices.push(
      <Notice key="stopped" tone="warning" title="Paused">
        It is not being scheduled, and anything waiting on it — a request, a client, its parent —
        waits too. Resume it from Control, or with SIGCONT.
      </Notice>,
    )
  }
  if (!gone && stale) {
    notices.push(
      <Notice
        key="stale"
        tone="warning"
        title={
          stale.binary
            ? "Running a binary that has since been replaced"
            : `Running ${plural(stale.libraries.length, "library", "libraries")} an upgrade replaced`
        }
      >
        <p>
          {stale.binary
            ? `The file it was started from changed after it started, so it is still running the old version. `
            : `${stale.libraries
                .slice(0, 3)
                .map((library) => library.path.split("/").pop())
                .join(
                  ", ",
                )}${stale.libraries.length > 3 ? " and more" : ""} changed on disk after it loaded ${
                stale.libraries.length === 1 ? "it" : "them"
              }. `}
          Restarting {restartWith} loads the new {stale.binary ? "version" : "code"}
          {stale.libraries.some((library) => /libssl|libcrypto/.test(library.path))
            ? ", which matters for a security fix to OpenSSL"
            : ""}
          .
        </p>
      </Notice>,
    )
  }
  for (const warning of warnings) {
    notices.push(
      <Notice key={warning.key} tone={warning.tone} title={warning.title}>
        <p>{warning.body}</p>
        {warning.key === "deleted" && (
          <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("files")}>
            Show the files
          </Button>
        )}
      </Notice>,
    )
  }
  if (notices.length === 0) return null
  return <div className="grid min-w-0 gap-3 lg:grid-cols-2">{notices}</div>
}

/**
 * Where the process sits, drawn the way the container page draws how a
 * container is reached (`docker/container-picture`): on the left what
 * supervises it — the unit, the PM2 application or the container that would
 * start it again — and its parent; the process in the middle as the product
 * it is, with its state in the mark's corner; on the right what it started and
 * every socket it listens on.
 *
 * A child's line pulses while it is busy, a port's while connections are open
 * on it; a supervisor's line is the brand's, because that is where the remedy
 * for a runaway child usually is — killing a worker that gunicorn or PM2
 * starts again at once is the loop this exists to break. Every node opens
 * what it is: a parent or a child its own page, the supervisor its page, a
 * port the Network tab. Narrower than `lg` the lanes stack and the lines are
 * not drawn, so each node says in words what it is.
 */
function ProcessPicture({ process: row, gone, tree, inspect, onOpen, onTab }: OverviewProps) {
  const ancestors = useMemo(() => tree?.ancestors ?? [], [tree])
  const children = useMemo(() => tree?.children ?? [], [tree])
  const live = !gone && row.state !== "zombie"

  const above = useMemo(() => {
    const nodes: PictureNode[] = []
    const href = managerHref(row)
    if (href) nodes.push(supervisorNode(row, href, live))
    const parent = ancestors.at(-1)
    if (parent) {
      const chain = ancestors.slice(0, -1)
      nodes.push({
        ...linkNode(parent, "Its parent", live, onOpen),
        hint: (
          <span className="numeric">
            PID {parent.pid}
            {chain.length > 0 && (
              <span className="text-muted-foreground/70">
                {" "}
                · under {chain.map((link) => link.name).join(" › ")}
              </span>
            )}
          </span>
        ),
        wire: { tone: "default", dashed: !live, carries: false },
      })
    } else if (row.ppid === 0) {
      nodes.push({
        id: "top",
        mark: (
          <WireMark tone="logo" size="md">
            <ProductGlyph id="linux" />
          </WireMark>
        ),
        eyebrow: "Started by",
        title: "The kernel",
        hint: "The top of its tree",
        wire: { tone: "default", dashed: true, carries: false },
      })
    }
    return nodes
  }, [row, ancestors, live, onOpen])

  const below = useMemo(() => {
    // Forty gunicorn workers are one thing to the reader, so the copies of a
    // program it started are one node, and the list under the picture has
    // each of them.
    const groups = childGroups(children)
    const nodes: PictureNode[] = groups.slice(0, CHILDREN_DRAWN).map((group) => {
      const link = group[0]
      if (group.length === 1) {
        return {
          ...linkNode(link, "Started by it", live, onOpen),
          hint: (
            <span className="numeric">
              PID {link.pid} ·{" "}
              <span className={cn(cpuTone(link.cpuPercent) !== "default" && "text-warning")}>
                {link.cpuReady === false ? "measuring" : `${percent(link.cpuPercent)} CPU`}
              </span>{" "}
              · {bytes(link.rss)}
            </span>
          ),
          wire: {
            tone: link.state === "zombie" ? "danger" : "default",
            dashed: !live || link.state === "zombie",
            carries: live && link.cpuPercent >= BUSY,
          },
        }
      }
      const cpu = group.reduce((n, l) => n + (l.cpuReady === false ? 0 : l.cpuPercent), 0)
      const rss = group.reduce((n, l) => n + l.rss, 0)
      const pids = group.map((l) => l.pid).sort((a, b) => a - b)
      return {
        ...linkNode(link, "Started by it", live, onOpen),
        id: `group:${link.name}`,
        title: (
          <span className="block truncate">
            {link.name}{" "}
            <span className="numeric font-normal text-muted-foreground">× {group.length}</span>
          </span>
        ),
        hint: (
          <span className="numeric">
            PIDs {pids[0]}–{pids.at(-1)} ·{" "}
            <span className={cn(cpuTone(cpu) !== "default" && "text-warning")}>
              {percent(cpu)} CPU
            </span>{" "}
            · {bytes(rss)}
          </span>
        ),
        open: () =>
          document
            .getElementById(CHILDREN_LIST)
            ?.scrollIntoView({ behavior: "smooth", block: "start" }),
        label: `Show the ${group.length} ${link.name} processes it started`,
        wire: {
          tone: group.some((l) => l.state === "zombie") ? "danger" : "default",
          dashed: !live,
          carries: live && cpu >= BUSY,
        },
      }
    })
    if (groups.length > CHILDREN_DRAWN) {
      const rest = groups.slice(CHILDREN_DRAWN).reduce((n, group) => n + group.length, 0)
      nodes.push({
        id: "more",
        mark: (
          <WirePlaceholder size="md">
            <Cpu />
          </WirePlaceholder>
        ),
        eyebrow: "Started by it",
        title: `${plural(rest, "more process", "more processes")}`,
        hint: "Listed under the picture",
        open: () =>
          document
            .getElementById(CHILDREN_LIST)
            ?.scrollIntoView({ behavior: "smooth", block: "start" }),
        label: "Show every process it started",
        wire: { tone: "default", dashed: true, carries: false },
      })
    }
    const connections = row.connections ?? 0
    const listening = row.listening ?? []
    if (listening.length > 0) {
      // One node for every socket it listens on: nginx on 80 and 443, over
      // IPv4 and IPv6, is one way in, and four nodes of it pushed the
      // picture past the window.
      const ports = [...new Set(listening.map((port) => port.port))].sort((a, b) => a - b)
      const protos = [
        ...new Set(listening.map((port) => port.proto.replace(/6$/, "").toUpperCase())),
      ]
      const open = listening.some((port) => reach(port.address) === "every interface")
      const local = listening.every((port) => reach(port.address) === "this machine only")
      nodes.push({
        id: "ports",
        mark: (
          <WireMark tone={open ? "warning" : "logo"} size="md">
            {open ? <Warning aria-hidden /> : <Globe aria-hidden />}
          </WireMark>
        ),
        eyebrow: `Listens · ${protos.join(" and ")}`,
        title: (
          <span className="block truncate font-mono">
            {ports
              .slice(0, 4)
              .map((port) => `:${port}`)
              .join(" ")}
            {ports.length > 4 && (
              <span className="font-sans text-muted-foreground"> +{ports.length - 4}</span>
            )}
          </span>
        ),
        hint: `${open ? "every interface" : local ? "this machine only" : "one address"} · ${plural(connections, "open connection")}`,
        open: () => onTab("network"),
        label: `Show what it listens on and its ${plural(connections, "connection")}`,
        wire: {
          tone: open ? "warning" : "default",
          dashed: !live,
          carries: live && connections > 0,
        },
      })
    }
    if ((row.listening ?? []).length === 0 && connections > 0) {
      nodes.push({
        id: "connections",
        mark: (
          <WireMark tone="logo" size="md">
            <NetworkDevice aria-hidden />
          </WireMark>
        ),
        eyebrow: "Connected",
        title: plural(connections, "open connection"),
        hint: "It listens on nothing; these are its own",
        open: () => onTab("network"),
        label: "Show its connections",
        wire: { tone: "default", dashed: !live, carries: live },
      })
    }
    if (live && !uncontrollable(row) && inspect?.files.ready && inspect.files.files > 0) {
      nodes.push({
        id: "files",
        mark: (
          <WireMark tone={inspect.files.deleted ? "warning" : "logo"} size="md">
            <FolderOpen aria-hidden />
          </WireMark>
        ),
        eyebrow: "Holds open",
        title: plural(inspect.files.files, "file"),
        hint:
          inspect.files.deleted > 0
            ? `${inspect.files.deleted} deleted since`
            : `${plural(inspect.files.total, "descriptor")} in all`,
        open: () => onTab("files"),
        label: "Show its open files",
        wire: {
          tone: inspect.files.deleted ? "warning" : "default",
          dashed: !live,
          carries: false,
        },
      })
    }
    return nodes
  }, [children, row, live, inspect, onOpen, onTab])

  const product = processProduct(row.name)
  const kind = uncontrollable(row)

  return (
    <WiringPicture
      title="Where it runs"
      left={{
        title: "Above it",
        nodes: above,
        empty: (
          <WireNode
            align="end"
            mark={
              <WirePlaceholder size="md">
                <Cpu />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Reading its parents…</span>}
          />
        ),
      }}
      right={{
        title: "Below and around it",
        nodes: below,
        empty: (
          <WireNode
            mark={
              <WirePlaceholder size="md">
                <Cpu />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Nothing under it</span>}
            hint="It has started no process and listens on no port"
          />
        ),
      }}
      center={{
        mark: (
          <>
            <WireMark tone="logo" size="lg">
              {hasProductLogo(product) ? (
                <ProductGlyph id={product} />
              ) : kind === "kernel" ? (
                <ProductGlyph id="linux" />
              ) : (
                <Cpu aria-hidden />
              )}
            </WireMark>
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background"
            >
              <StatusDot
                state={gone ? "stopped" : processStateTone(row.state)}
                live={!gone && row.state === "running"}
              />
            </span>
          </>
        ),
        eyebrow: `PID ${row.pid}`,
        title: <span className="block truncate">{row.name}</span>,
        hint: (
          <span className="numeric">
            {gone
              ? gone
              : kind === "zombie"
                ? "exited"
                : `${row.cpuReady === false ? "measuring" : `${percent(row.cpuPercent)} CPU`} · ${bytes(row.rss)}`}
          </span>
        ),
      }}
    />
  )
}

/** The process's supervisor as the product it runs, opening its page. */
function supervisorNode(row: ProcessRow, href: string, live: boolean): PictureNode {
  const name = ownerName(row)
  const product =
    row.manager === "systemd"
      ? (unitProduct(row.managerName ?? "") ?? "systemd")
      : row.manager === "pm2"
        ? "pm2"
        : (processProduct(row.managerLabel ?? "") ?? "docker")
  const eyebrow =
    row.manager === "systemd"
      ? "systemd unit · restarts it"
      : row.manager === "pm2"
        ? "PM2 application · restarts it"
        : "Docker container · runs it"
  return {
    id: "owner",
    mark: (
      <WireMark tone="logo" size="md">
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Cpu aria-hidden />}
      </WireMark>
    ),
    eyebrow,
    title: <span className="block truncate">{name}</span>,
    hint: (
      <span className="inline-flex items-center gap-1">
        Open {managerName(row.manager) === "Container" ? "the container" : name}
        <ArrowRight aria-hidden className="size-3" />
      </span>
    ),
    href,
    label: `Open ${name}`,
    wire: { tone: "default", dashed: !live, carries: false },
  }
}

function linkNode(
  link: ProcessLink,
  eyebrow: string,
  live: boolean,
  onOpen: (pid: number) => void,
): PictureNode {
  const product = processProduct(link.name)
  return {
    id: `pid:${link.pid}`,
    mark: (
      <span className="relative flex">
        <WireMark tone="logo" size="md">
          {hasProductLogo(product) ? (
            <ProductGlyph id={product} />
          ) : link.pid === 1 ? (
            <ProductGlyph id="systemd" />
          ) : (
            <Cpu aria-hidden />
          )}
        </WireMark>
        <span
          aria-hidden
          className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
        >
          <StatusDot state={processStateTone(link.state)} />
        </span>
      </span>
    ),
    eyebrow,
    title: <span className="block truncate">{link.name}</span>,
    open: () => onOpen(link.pid),
    label: `Open ${link.name} (${link.pid})`,
    wire: { tone: "default", dashed: !live, carries: false },
  }
}

/** The command as it was run, coloured as a command is, with where it runs from. */
function CommandBlock({
  process: row,
  inspect,
}: {
  process: ProcessRow
  inspect?: ProcessInspect
}) {
  if (!row.cmdline && !row.exe) return null
  const argv = inspect?.argv ?? []
  const root = inspect?.root
  return (
    <Section
      title="Command"
      actions={
        row.cmdline && (
          <IconAction
            label="Copy command line"
            onClick={() => void copyText(row.cmdline, "Command line copied")}
          >
            <Copy />
          </IconAction>
        )
      }
    >
      {row.cmdline && (
        <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
          <ShellWords command={row.cmdline} />
        </Well>
      )}
      <DetailList>
        {argv.length > 1 && (
          <Detail label="Arguments" className="numeric">
            {plural(argv.length - 1, "argument")} after the program
          </Detail>
        )}
        <Detail label="Executable" className="font-mono break-all">
          {row.exe || inspect?.exe || "Not readable"}
          {inspect?.exeDeleted && (
            <span className="ml-2 font-sans text-warning">replaced on disk since it started</span>
          )}
        </Detail>
        <Detail label="Working directory" className="font-mono break-all">
          {row.cwd ? (
            <Link href={`/files?path=${encodeURIComponent(row.cwd)}`} className="hover:underline">
              {row.cwd}
            </Link>
          ) : (
            "Not readable"
          )}
        </Detail>
        {root && root !== "/" && (
          <Detail label="Root directory" className="font-mono break-all">
            {root}{" "}
            <span className="font-sans text-muted-foreground">· a chroot or a container</span>
          </Detail>
        )}
        <Detail label="Started" className="numeric">
          {timestamp(row.createTime)}
        </Detail>
      </DetailList>
    </Section>
  )
}

function WhoItRunsAs({
  process: row,
  inspect,
  onTab,
}: {
  process: ProcessRow
  inspect?: ProcessInspect
  onTab: (tab: string) => void
}) {
  const id = inspect?.identity
  const own = inspect ? namespaceWords(inspect.namespaces) : []
  const sec = inspect?.security
  const changed =
    id?.ready && (id.user.id !== id.effectiveUser.id || id.group.id !== id.effectiveGroup.id)
  return (
    <Section title="Who it runs as">
      <DetailList>
        <Detail label="User" className="font-mono">
          {id?.ready ? idWords(id.user) : row.username || "unknown"}
          {changed && (
            <span className="ml-2 font-sans text-warning">acts as {idWords(id.effectiveUser)}</span>
          )}
        </Detail>
        {id?.ready && (
          <>
            <Detail label="Group" className="font-mono">
              {idWords(id.group)}
            </Detail>
            {id.groups.length > 0 && (
              <Detail label="Also in" className="font-mono break-words">
                {id.groups
                  .filter((g) => g.id !== id.group.id)
                  .map((g) => g.name || g.id)
                  .join(", ") || "no other group"}
              </Detail>
            )}
            <Detail label="Terminal">
              {id.terminal ? (
                <span className="font-mono">
                  {id.terminal}
                  {id.foreground ? (
                    <span className="font-sans text-muted-foreground"> · in the foreground</span>
                  ) : null}
                </span>
              ) : (
                <span className="text-muted-foreground">none — it runs detached</span>
              )}
            </Detail>
            <Detail label="Session · group" className="numeric font-mono">
              {id.session} · {id.processGroup}
            </Detail>
          </>
        )}
        <Detail label="Supervised by">
          {managerName(row.manager)}
          {ownerName(row) && row.manager !== "kernel" ? (
            <span className="text-muted-foreground"> · {ownerName(row)}</span>
          ) : null}
        </Detail>
        {inspect && (
          <Detail label="Isolation">
            {own.length === 0 ? (
              <span className="text-muted-foreground">shares everything with the host</span>
            ) : (
              <>
                its own {own.join(", ")}
                {inspect.namespacePid ? (
                  <span className="numeric text-muted-foreground">
                    {" "}
                    · PID {inspect.namespacePid} inside
                  </span>
                ) : null}
              </>
            )}
          </Detail>
        )}
        {inspect?.cgroup.path && (
          <Detail label="Control group" className="font-mono break-all">
            {inspect.cgroup.path}
          </Detail>
        )}
        {sec?.ready && (
          <Detail label="Privileges">
            {sec.allCapabilities ? (
              <span className="text-warning">every capability — root&rsquo;s</span>
            ) : sec.capabilities.length === 0 ? (
              <span className="text-muted-foreground">none beyond an ordinary user&rsquo;s</span>
            ) : (
              <button
                type="button"
                onClick={() => onTab("limits")}
                className="rounded-sm text-left focus-ring hover:underline"
              >
                {plural(sec.capabilities.length, "capability", "capabilities")}
              </button>
            )}
            {sec.seccomp === "filter" && (
              <span className="text-muted-foreground"> · system calls filtered</span>
            )}
          </Detail>
        )}
      </DetailList>
    </Section>
  )
}

function HowItIsScheduled({
  process: row,
  inspect,
  gone,
}: {
  process: ProcessRow
  inspect?: ProcessInspect
  gone?: string
}) {
  const s = inspect?.scheduling
  const io = inspect?.io
  const now = useNow(10_000, !gone)
  const up = Math.max(0, (now - new Date(row.createTime).getTime()) / 1000)
  return (
    <Section title="How it is scheduled">
      <DetailList>
        <Detail label="Priority">
          nice <span className="numeric font-mono">{row.nice}</span>
          {s?.ready && s.policy !== "normal" && (
            <span className="text-warning">
              {" "}
              · {s.policy}
              {s.realtimePriority ? ` ${s.realtimePriority}` : ""}
            </span>
          )}
        </Detail>
        {s?.ready && (
          <>
            <Detail label="Processors">
              {s.cpuCount > 0 && s.cpuCount < s.hostCpus ? (
                <span className="text-warning">
                  {s.cpuCount} of {s.hostCpus} · <span className="font-mono">{s.cpus}</span>
                </span>
              ) : (
                <span>any of {s.hostCpus}</span>
              )}
              {!gone && (
                <span className="numeric text-muted-foreground"> · last on CPU {s.lastCpu}</span>
              )}
            </Detail>
            <Detail label="Processor time" className="numeric">
              {duration(s.userSeconds + s.systemSeconds)}
              <span className="text-muted-foreground">
                {" "}
                · {percent(up > 0 ? ((s.userSeconds + s.systemSeconds) / up) * 100 : 0)} of its life
                ·{" "}
                {percent(
                  s.userSeconds + s.systemSeconds > 0
                    ? (s.systemSeconds / (s.userSeconds + s.systemSeconds)) * 100
                    : 0,
                )}{" "}
                in the kernel
              </span>
            </Detail>
            {s.ioWaitSeconds > 0 && (
              <Detail label="Waiting on disk" className="numeric">
                {duration(s.ioWaitSeconds)}
              </Detail>
            )}
            {inspect?.waitChannel && row.state !== "running" && (
              <Detail label="Waiting in" className="font-mono">
                {inspect.waitChannel}
              </Detail>
            )}
          </>
        )}
      </DetailList>
      {s?.ready && (
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 pt-1">
          <Counter label="Gave way to wait">{count(s.voluntarySwitches)}</Counter>
          <Counter label="Made to give way">{count(s.involuntarySwitches)}</Counter>
          <Counter label="Page faults">{count(s.minorFaults + s.majorFaults)}</Counter>
          <Counter label="Of them from disk">{count(s.majorFaults)}</Counter>
          <Counter label="Read from disk">{bytes(io?.readBytes ?? row.ioReadBytes ?? 0)}</Counter>
          <Counter label="Written to disk">
            {bytes(io?.writeBytes ?? row.ioWriteBytes ?? 0)}
          </Counter>
          {io?.ready && (
            <>
              <Counter label="Read in all">{bytes(io.charsRead)}</Counter>
              <Counter label="Written in all">{bytes(io.charsWritten)}</Counter>
            </>
          )}
        </dl>
      )}
    </Section>
  )
}

/** A counter that runs to millions, as a figure that fits its cell: 1.2M. */
const COMPACT = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 })
function count(value: number) {
  return value < 10_000 ? value.toLocaleString("en-US") : COMPACT.format(value)
}

const MEMORY_COLORS: Record<string, string> = {
  anonymous: "var(--chart-2)",
  file: "var(--chart-5)",
  shared: "var(--chart-4)",
  swap: "var(--chart-3)",
}

/**
 * Where its memory goes, as one strip of the parts the kernel splits it into
 * and a list of them that does not grow from row to row, then the three
 * figures that answer "what would ending it give back": what only it holds,
 * its fair share of what it shares, and the most it has ever held.
 */
function MemoryBlock({ process: row, inspect }: { process: ProcessRow; inspect?: ProcessInspect }) {
  const memory = inspect?.memory
  if (!memory?.ready) return null
  const parts = memoryParts(memory)
  const whole = parts.reduce((n, part) => n + part.count, 0)
  const oom = memory.oomReady ? oomWords(memory.oomScore, memory.oomScoreAdj) : undefined
  return (
    <Section title="Memory">
      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <div className="flex min-w-0 flex-col gap-3">
          <SplitBar
            label={`Its memory: ${parts.map((p) => `${p.label} ${bytes(p.count)}`).join(", ")}`}
            parts={parts.map((part) => ({
              key: part.key,
              count: part.count,
              color: MEMORY_COLORS[part.key],
            }))}
          />
          <RankList
            items={parts
              .filter((part) => part.count > 0)
              .map((part) => ({
                key: part.key,
                label: part.label,
                mono: false,
                swatch: MEMORY_COLORS[part.key],
                value: bytes(part.count),
                share: whole > 0 ? part.count / whole : 0,
              }))}
          />
        </div>
        <DetailList className="content-start">
          {memory.rollupReady && (
            <>
              <Detail label="Only its own">
                <span className="numeric font-mono">{bytes(memory.unique)}</span>
                <span className="text-muted-foreground"> · what ending it gives back</span>
              </Detail>
              <Detail label="Its fair share">
                <span className="numeric font-mono">{bytes(memory.proportional)}</span>
                <span className="text-muted-foreground">
                  {" "}
                  · shared pages split between their users
                </span>
              </Detail>
            </>
          )}
          <Detail label="Most it has held">
            <span className="numeric font-mono">{bytes(memory.peak)}</span>
            {memory.peak > row.rss * 1.5 && (
              <span className="text-muted-foreground"> · {bytes(row.rss)} now</span>
            )}
          </Detail>
          <Detail label="Address space" className="numeric font-mono">
            {bytes(memory.virtual)}
          </Detail>
          {memory.locked > 0 && (
            <Detail label="Locked in RAM" className="numeric font-mono">
              {bytes(memory.locked)}
            </Detail>
          )}
          {oom && (
            <Detail label="When memory runs out">
              <span
                className={cn(
                  oom.tone === "warning" && "text-warning",
                  oom.tone === "success" && "text-success",
                )}
              >
                {oom.word}
              </span>
              <span className="numeric text-muted-foreground">
                {" "}
                · scored {memory.oomScore}
                {memory.oomScoreAdj !== 0 ? `, bias ${memory.oomScoreAdj}` : ""}
              </span>
            </Detail>
          )}
        </DetailList>
      </div>
    </Section>
  )
}
