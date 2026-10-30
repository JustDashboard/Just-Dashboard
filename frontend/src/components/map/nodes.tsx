"use client"

import { memo, type ReactNode } from "react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"
import {
  Archive,
  ArrowRight,
  Clock,
  Code,
  Database,
  Globe,
  Layers,
  Lightning,
  ListUnordered,
  Linked,
  LockClosed,
  Logs,
  SettingsGear,
  Terminal,
} from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { kindLabel } from "@/lib/server-map"
import {
  ROLE_LABEL,
  SYSTEM_LABEL,
  type Role,
  type StackFrame,
  type SystemKind,
  type SystemNode,
} from "@/lib/server-systems"
import type { MapItem } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ItemMark, TONE_DOT } from "@/components/map/marks"

/**
 * The shapes on the map, one for each kind of thing, so a reader tells a
 * domain from a database before reading either name — the way a workflow
 * editor's nodes are told apart by their outline and their mark.
 *
 * Every shape is the product's own vocabulary on a canvas: a `ProductLogo`
 * tile, the name, a `Status` dot and word, the eyebrow voice for what kind it
 * is. What makes each its own is its outline: the deployment is the large card
 * at the middle of its system; where traffic arrives is rounded on its left
 * like a workflow's trigger; a database is a cylinder; a web container wears a
 * browser's bar, a proxy is cut to a gateway's point, a volume is a disk; what
 * hangs under a node — a checkout, the logs, a certificate — is a round
 * sub-node on a dashed line, n8n's tools under its agent.
 */

export type NodeState = "chosen" | "lit" | "dim" | "hit" | undefined

export type Port = { id: string; label: string; left: number }

export type FaceData = {
  node: SystemNode
  state: NodeState
  /** The sub-node ports along its foot, where its satellites hang. */
  ports: Port[]
  /** Which of its sides a line arrives at or leaves from. */
  sides: { in: boolean; out: boolean }
}

export type FrameData = {
  kind: SystemKind
  title: string
  anchor: MapItem
  anchorKind: string
  census: string
  chosen: boolean
  dimmed: boolean
}

export type StackData = { frame: StackFrame; dimmed: boolean }

type FaceProps = NodeProps<Node<FaceData>>

/** The outline every rectangular shape shares, and what choosing or dimming does to it. */
function shell(state: NodeState, tone: MapItem["tone"]) {
  return cn(
    "relative h-full w-full border bg-card text-left transition-[border-color,opacity,box-shadow] duration-150",
    tone === "danger"
      ? "border-rule-danger"
      : tone === "warning"
        ? "border-rule-warning"
        : "border-border",
    state === "chosen" && "border-brand ring-2 ring-rule-brand",
    state === "lit" && "border-rule-brand",
    state === "hit" && "ring-2 ring-signal",
    state === "dim" && "opacity-35",
  )
}

function Ports({ data }: { data: FaceData }) {
  return (
    <>
      <Handle
        type="target"
        position={Position.Left}
        id="in"
        isConnectable={false}
        className={cn("map-port map-port-in", !data.sides.in && "map-port-off")}
      />
      <Handle
        type="source"
        position={Position.Right}
        id="out"
        isConnectable={false}
        className={cn("map-port map-port-out", !data.sides.out && "map-port-off")}
      />
      <Handle
        type="source"
        position={Position.Bottom}
        id="down"
        isConnectable={false}
        className="map-port map-port-off"
      />
      <Handle
        type="target"
        position={Position.Top}
        id="up"
        isConnectable={false}
        className="map-port map-port-off"
      />
      {data.ports.map((port) => (
        <Handle
          key={port.id}
          type="source"
          position={Position.Bottom}
          id={`sub:${port.id}`}
          isConnectable={false}
          className="map-port map-port-sub"
          style={{ left: port.left }}
        >
          <span className="map-port-label pointer-events-none absolute top-3 left-3 text-micro font-medium whitespace-nowrap text-muted-foreground">
            {port.label}
          </span>
        </Handle>
      ))}
    </>
  )
}

function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("eyebrow block truncate", className)}>{children}</span>
}

/** A box's name: the one line a box always has room for. */
function Name({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("map-name block truncate font-semibold text-foreground", className)}>
      {children}
    </span>
  )
}

/** A box's state: its dot and its word, cut short rather than run out of the box. */
function State({ item, className }: { item: MapItem; className?: string }) {
  return (
    <Status
      tone={TONE_DOT[item.tone]}
      label={item.state}
      className={cn("max-w-full min-w-0 text-hint [&>span:last-child]:truncate", className)}
    />
  )
}

/**
 * What another system draws too — one Postgres three deployments use — says
 * so on its corner, so a reader knows a box is not this system's alone
 * before choosing it; the inspector names the others.
 */
function SharedMark({ node }: { node: SystemNode }) {
  if (node.shared.length === 0) return null
  return (
    <span
      title={`Also drawn in ${node.shared.length === 1 ? "one other system" : `${node.shared.length} other systems`}`}
      className="absolute -top-2.5 right-3 z-10 flex items-center gap-1 rounded-sm border border-hairline bg-card px-1.5 text-micro leading-4 text-muted-foreground"
    >
      <Linked className="size-3" />
      also in {node.shared.length}
    </span>
  )
}

function factOf(item: MapItem, label: string) {
  return item.facts?.find((f) => f.label === label)?.value
}

/** "1 container", "3 containers": the fact is the bare count. */
function containerCount(fact: string | undefined) {
  if (!fact) return "no containers"
  return /^1$/.test(fact.trim()) ? "1 container" : `${fact} containers`
}

// ---------------------------------------------------------------------------
// The shapes.
// ---------------------------------------------------------------------------

/** A deployment, or this dashboard: what its system is, and the largest box on it. */
export const HeroNode = memo(function HeroNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const dashboard = node.kind === "dashboard"
  const environment = factOf(item, "Environment")
  const address = dashboard ? undefined : factOf(item, "Address")
  const extra = dashboard
    ? [factOf(item, "Newest") && `newest ${factOf(item, "Newest")}`]
    : [
        factOf(item, "Framework"),
        factOf(item, "Services") && `${factOf(item, "Services")} services`,
        factOf(item, "Port") && `port ${factOf(item, "Port")}`,
        factOf(item, "Last run") && `last run ${factOf(item, "Last run")}`,
      ]
  return (
    <div className={cn(shell(state, item.tone), "flex flex-col rounded-xl")}>
      <Ports data={data} />
      <SharedMark node={node} />
      <div className="flex items-start gap-3 p-4 pb-3">
        <ItemMark item={item} kind={node.kind} size="lg" />
        <div className="min-w-0 flex-1 pt-0.5">
          <Eyebrow>
            {dashboard ? "This dashboard" : "Deployment"}
            {environment && ` · ${environment}`}
          </Eyebrow>
          <Name className="mt-0.5 text-title leading-6">{item.name}</Name>
          <State item={item} className="mt-1" />
        </div>
      </div>
      <div className="mt-auto space-y-1 border-t border-hairline px-4 py-3">
        <span className="flex items-center gap-1.5 truncate font-mono text-hint text-foreground">
          <Globe className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{address ?? item.detail ?? "no address"}</span>
        </span>
        <span className="block truncate text-hint text-muted-foreground">
          {extra.filter(Boolean).join(" · ") || " "}
        </span>
      </div>
    </div>
  )
})

/** A Compose stack that is a system of its own: its card at the head of its members. */
export const StackNode = memo(function StackNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  return (
    <div className={cn(shell(state, item.tone), "flex flex-col rounded-xl")}>
      <Ports data={data} />
      <SharedMark node={node} />
      <div className="flex items-start gap-3 p-4">
        <ProductLogo id="docker-compose" size="md" fallback={Layers} />
        <div className="min-w-0 flex-1">
          <Eyebrow>Compose stack</Eyebrow>
          <Name className="mt-0.5 text-title">{item.name}</Name>
          <State item={item} className="mt-1" />
        </div>
      </div>
      <div className="mt-auto flex items-center gap-2 border-t border-hairline px-4 py-2">
        <ItemMark item={item} kind={node.kind} size="sm" className="size-5 rounded-md" />
        <span className="truncate text-hint text-muted-foreground">
          {containerCount(factOf(item, "Containers"))}
        </span>
      </div>
    </div>
  )
})

const ENTRY_GLYPH = { domains: Globe, streams: ArrowRight, tunnels: LockClosed }

/**
 * Where traffic arrives: a domain, a stream, a tailnet, the dashboard's own
 * address. Rounded on the left the way a workflow's trigger is, because the
 * system starts here.
 */
export const EntryNode = memo(function EntryNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const site = node.itemId === "dashboard:site"
  const eyebrow = site
    ? "Dashboard address"
    : node.kind === "tunnels"
      ? "Private network"
      : node.kind === "streams"
        ? "TCP stream"
        : "Domain"
  return (
    <div
      className={cn(
        shell(state, item.tone),
        "flex items-center gap-3 rounded-l-[34px] rounded-r-xl pr-4 pl-3",
      )}
    >
      <Ports data={data} />
      <SharedMark node={node} />
      {item.product ? (
        <ItemMark item={item} kind={node.kind} className="rounded-full" />
      ) : (
        <span className="flex size-10 shrink-0 items-center justify-center rounded-full border border-hairline bg-background">
          {(() => {
            const Glyph = site
              ? LockClosed
              : (ENTRY_GLYPH[node.kind as keyof typeof ENTRY_GLYPH] ?? Globe)
            return <Glyph className="size-4.5 text-muted-foreground" />
          })()}
        </span>
      )}
      <div className="min-w-0 flex-1">
        <Eyebrow>{eyebrow}</Eyebrow>
        <Name className="font-mono text-body font-medium">{item.name}</Name>
        <State item={item} />
      </div>
    </div>
  )
})

const ROLE_GLYPH: Partial<Record<Role, React.ComponentType<{ className?: string }>>> = {
  api: Code,
  worker: SettingsGear,
  cache: Lightning,
  queue: ListUnordered,
  database: Database,
}

/** A container, drawn as what it is for. */
export const RuntimeNode = memo(function RuntimeNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const role = node.role ?? "app"
  const image = factOf(item, "Image") ?? item.detail
  const ports = factOf(item, "Published")
  const up = factOf(item, "Up for")
  const RoleGlyph = ROLE_GLYPH[role]
  const body = (
    <div className="flex min-w-0 flex-1 items-center gap-3 px-3">
      <ItemMark item={item} kind={node.kind} />
      <div className="min-w-0 flex-1">
        <Eyebrow className="flex items-center gap-1">
          {RoleGlyph && <RoleGlyph className="size-3 shrink-0" />}
          {ROLE_LABEL[role]} · container
        </Eyebrow>
        <Name className="text-body">{item.name}</Name>
        <span className="flex min-w-0 items-center gap-2">
          <State item={item} />
          {(ports || up) && (
            <span className="truncate font-mono text-micro text-muted-foreground">
              {ports ? `:${ports.split("→")[0]}` : `up ${up}`}
            </span>
          )}
        </span>
      </div>
    </div>
  )
  if (role === "database") {
    return (
      <Cylinder state={state} tone={item.tone} cap={12}>
        <Ports data={data} />
        <SharedMark node={node} />
        {body}
      </Cylinder>
    )
  }
  if (role === "proxy") {
    return (
      <Gateway state={state} tone={item.tone}>
        <Ports data={data} />
        <SharedMark node={node} />
        <div className="flex h-full items-center px-3">{body}</div>
      </Gateway>
    )
  }
  return (
    <div
      className={cn(
        shell(state, item.tone),
        "flex flex-col",
        role === "cache" || role === "queue" ? "rounded-[22px]" : "rounded-xl",
      )}
    >
      <Ports data={data} />
      <SharedMark node={node} />
      {role === "web" && (
        // A browser's bar: three dots and the image in the address field.
        <span className="flex h-6 shrink-0 items-center gap-1.5 border-b border-hairline px-3">
          <span className="size-1.5 rounded-full bg-border-strong" />
          <span className="size-1.5 rounded-full bg-border-strong" />
          <span className="size-1.5 rounded-full bg-border-strong" />
          <span className="ml-2 min-w-0 flex-1 truncate rounded-sm bg-background px-2 font-mono text-micro leading-4 text-muted-foreground">
            {image}
          </span>
        </span>
      )}
      <div className="flex min-h-0 flex-1 items-center">{body}</div>
      {role !== "web" && role !== "cache" && role !== "queue" && image && (
        <span className="block truncate border-t border-hairline px-3 py-1 font-mono text-micro text-muted-foreground">
          {image}
        </span>
      )}
    </div>
  )
})

/** A systemd service or a PM2 app: a process on the host, drawn as a terminal's window. */
export const ProcessNode = memo(function ProcessNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const pm2 = node.kind === "pm2"
  return (
    <div className={cn(shell(state, item.tone), "flex flex-col rounded-xl")}>
      <Ports data={data} />
      <SharedMark node={node} />
      <span className="flex h-6 shrink-0 items-center gap-1.5 border-b border-hairline bg-background/60 px-3 font-mono text-micro text-muted-foreground">
        <Terminal className="size-3" />
        {pm2 ? "pm2" : "systemd"}
        {item.detail && <span className="truncate">· {item.detail}</span>}
      </span>
      <div className="flex min-h-0 flex-1 items-center gap-3 px-3">
        <ItemMark item={item} kind={node.kind} />
        <div className="min-w-0 flex-1">
          <Eyebrow>{pm2 ? "PM2 app" : "Service"}</Eyebrow>
          <Name className="text-body">{item.name}</Name>
          <State item={item} />
        </div>
      </div>
    </div>
  )
})

/** A database: a cylinder, its engine's mark, where it runs and how fast it answered. */
export const DatastoreNode = memo(function DatastoreNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const store = node.itemId === "dashboard:store"
  const engine = factOf(item, "Engine") ?? item.detail
  const answered = factOf(item, "Answered in")
  const where = factOf(item, "Runs in")
  return (
    <Cylinder state={state} tone={item.tone} cap={16}>
      <Ports data={data} />
      <SharedMark node={node} />
      <div className="flex min-w-0 flex-1 items-center gap-3 px-4 pt-2">
        <ItemMark item={item} kind={store ? "databases" : node.kind} />
        <div className="min-w-0 flex-1">
          <Eyebrow>{store ? "Dashboard store" : "Database"}</Eyebrow>
          <Name className="text-sm">{item.name}</Name>
          <span className="flex min-w-0 items-center gap-2">
            <State item={item} />
            {answered && (
              <span className="truncate font-mono text-micro text-muted-foreground">
                {answered}
              </span>
            )}
          </span>
          <span className="mt-0.5 block truncate font-mono text-micro text-muted-foreground">
            {[engine, where && `in ${where}`].filter(Boolean).join(" · ")}
          </span>
        </div>
      </div>
    </Cylinder>
  )
})

/** A volume: a disk, its name and its size. */
export const VolumeNode = memo(function VolumeNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  return (
    <div
      className={cn(shell(state, item.tone), "flex items-center gap-3 rounded-[18px] pr-4 pl-2")}
    >
      <Ports data={data} />
      <SharedMark node={node} />
      <span className="relative flex size-9 shrink-0 items-center justify-center rounded-full border border-hairline bg-background">
        <span className="absolute inset-[7px] rounded-full border border-border-strong" />
        <span className="size-1.5 rounded-full bg-muted-foreground" />
      </span>
      <div className="min-w-0 flex-1">
        <Name className="font-mono text-hint font-medium">{item.name}</Name>
        <span className="flex min-w-0 items-center gap-2">
          <State item={item} />
          {item.detail && (
            <span className="truncate font-mono text-micro text-muted-foreground">
              {item.detail}
            </span>
          )}
        </span>
      </div>
    </div>
  )
})

/** A backup job: when it last ran, when it runs next, where the copy goes. */
export const BackupNode = memo(function BackupNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const destination = factOf(item, "Destination")
  return (
    <div className={cn(shell(state, item.tone), "flex flex-col rounded-xl")}>
      <Ports data={data} />
      <SharedMark node={node} />
      <div className="flex min-h-0 flex-1 items-center gap-3 px-3">
        <ProductLogo id={item.product} fallback={Archive} />
        <div className="min-w-0 flex-1">
          <Eyebrow>Backup</Eyebrow>
          <Name className="text-body">{item.name}</Name>
          <State item={item} />
        </div>
      </div>
      <span className="flex items-center gap-1.5 truncate border-t border-hairline px-3 py-1.5 text-micro text-muted-foreground">
        <Clock className="size-3 shrink-0" />
        <span className="truncate">
          {[item.detail, destination].filter(Boolean).join(" · ") || "no schedule"}
        </span>
      </span>
    </div>
  )
})

const SATELLITE_GLYPH: Record<string, React.ComponentType<{ className?: string }>> = {
  logs: Logs,
  certificates: LockClosed,
}

/**
 * What hangs under a node: the checkout it is built from, its logs, a
 * domain's certificate. A round sub-node with its name under it.
 */
export const SatelliteNode = memo(function SatelliteNode({ data }: FaceProps) {
  const { node, state } = data
  const { item } = node
  const Glyph = SATELLITE_GLYPH[node.kind]
  const logs = node.kind === "logs"
  return (
    <div
      className={cn(
        "flex h-full w-full flex-col items-center text-center transition-opacity",
        state === "dim" && "opacity-35",
      )}
    >
      <Handle
        type="target"
        position={Position.Top}
        id="up"
        isConnectable={false}
        className="map-port map-port-off"
      />
      <Handle
        type="target"
        position={Position.Left}
        id="in"
        isConnectable={false}
        className="map-port map-port-off"
        style={{ top: 26 }}
      />
      <Handle
        type="source"
        position={Position.Right}
        id="out"
        isConnectable={false}
        className={cn("map-port map-port-out", !data.sides.out && "map-port-off")}
        style={{ top: 26 }}
      />
      <span
        className={cn(
          "flex size-[52px] shrink-0 items-center justify-center rounded-full border bg-card transition-[border-color,box-shadow]",
          item.tone === "danger"
            ? "border-rule-danger"
            : item.tone === "warning"
              ? "border-rule-warning"
              : "border-border",
          state === "chosen" && "border-brand ring-2 ring-rule-brand",
          state === "lit" && "border-rule-brand",
          state === "hit" && "ring-2 ring-signal",
        )}
      >
        {Glyph ? (
          <Glyph className={cn("size-5", logs ? "text-foreground" : "text-muted-foreground")} />
        ) : (
          <ItemMark item={item} kind={node.kind} size="sm" className="border-0 bg-transparent" />
        )}
      </span>
      <Name className="mt-1.5 w-full text-hint font-medium">
        {node.kind === "certificates" ? "Certificate" : item.name}
      </Name>
      <span
        className={cn(
          "block w-full truncate text-micro",
          item.tone === "danger"
            ? "text-destructive"
            : item.tone === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      >
        {logs
          ? "open the output"
          : node.kind === "repositories"
            ? (item.detail ?? item.state)
            : item.state}
      </span>
    </div>
  )
})

// ---------------------------------------------------------------------------
// Outlines a rectangle cannot draw.
// ---------------------------------------------------------------------------

function outline(state: NodeState, tone: MapItem["tone"]) {
  return state === "chosen"
    ? "var(--color-brand)"
    : state === "lit"
      ? "var(--color-rule-brand)"
      : tone === "danger"
        ? "var(--color-rule-danger)"
        : tone === "warning"
          ? "var(--color-rule-warning)"
          : "var(--color-border)"
}

/** A cylinder: an ellipse for its lid, straight sides, a curved foot. */
function Cylinder({
  state,
  tone,
  cap,
  children,
}: {
  state: NodeState
  tone: MapItem["tone"]
  cap: number
  children: ReactNode
}) {
  const stroke = outline(state, tone)
  return (
    <div
      className={cn(
        "relative flex h-full w-full flex-col transition-opacity",
        state === "dim" && "opacity-35",
      )}
    >
      <span
        aria-hidden="true"
        className="absolute inset-0 border bg-card"
        style={{
          borderColor: stroke,
          borderRadius: `50% / ${cap}px`,
          boxShadow: state === "chosen" ? "0 0 0 2px var(--color-rule-brand)" : undefined,
        }}
      />
      <span
        aria-hidden="true"
        className="absolute inset-x-0 top-0 border bg-control"
        style={{ height: cap * 2, borderColor: stroke, borderRadius: "50%" }}
      />
      <div className="relative flex min-h-0 flex-1 items-center" style={{ paddingTop: cap * 1.4 }}>
        {children}
      </div>
    </div>
  )
}

/** A gateway: a box cut to a point on both ends, for what stands in front of the rest. */
function Gateway({
  state,
  tone,
  children,
}: {
  state: NodeState
  tone: MapItem["tone"]
  children: ReactNode
}) {
  const stroke = outline(state, tone)
  return (
    <div
      className={cn("relative h-full w-full transition-opacity", state === "dim" && "opacity-35")}
    >
      <svg
        aria-hidden="true"
        viewBox="0 0 100 100"
        preserveAspectRatio="none"
        className="pointer-events-none absolute inset-0 h-full w-full overflow-visible"
      >
        <polygon
          points="7,0 93,0 100,50 93,100 7,100 0,50"
          fill="var(--color-card)"
          stroke={stroke}
          strokeWidth={state === "chosen" ? 2 : 1}
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="relative h-full">{children}</div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// The frames.
// ---------------------------------------------------------------------------

/**
 * A system's frame: its mark, its name, what anchors it and what it holds.
 * The ground inside is a step up from the canvas's so one system reads as one
 * thing among the others, and its head is the handle the whole diagram is
 * carried by (the canvas picks it up from `map-frame-grip`) — `nopan` so
 * pressing it does not start a pan instead.
 */
export const SystemFrameNode = memo(function SystemFrameNode({ data }: NodeProps<Node<FrameData>>) {
  const loose = data.kind === "loose"
  return (
    <div
      className={cn(
        "map-frame relative h-full w-full rounded-[28px] border transition-[border-color,opacity]",
        data.chosen ? "border-rule-brand" : "border-hairline",
        loose && "border-dashed",
        data.dimmed && "opacity-40",
      )}
    >
      <div
        title="Drag to move this system"
        className="map-frame-grip nopan nodrag flex h-16 items-center gap-3 rounded-t-[28px] border-b border-hairline px-6"
      >
        {loose ? (
          <span className="flex size-10 items-center justify-center rounded-lg border border-hairline bg-background">
            <Layers className="size-4 text-muted-foreground" />
          </span>
        ) : (
          <ItemMark item={data.anchor} kind={data.anchorKind} />
        )}
        <div className="min-w-0 flex-1">
          <span className="map-name block truncate text-title font-semibold text-foreground">
            {data.title}
          </span>
          <span className="block truncate text-hint text-muted-foreground">
            {SYSTEM_LABEL[data.kind]}
            {data.census && ` · ${data.census}`}
          </span>
        </div>
        {!loose && (
          <Status
            tone={TONE_DOT[data.anchor.tone]}
            label={data.anchor.state}
            className="max-w-[40%] min-w-0 text-hint [&>span:last-child]:truncate"
          />
        )}
      </div>
    </div>
  )
})

/** A Compose stack a deployment or this dashboard runs: the frame round its containers. */
export const StackFrameNode = memo(function StackFrameNode({ data }: NodeProps<Node<StackData>>) {
  const { item } = data.frame
  return (
    <div
      className={cn(
        "h-full w-full rounded-xl border border-border bg-background/70 transition-opacity",
        data.dimmed && "opacity-40",
      )}
    >
      <Handle
        type="target"
        position={Position.Left}
        id="in"
        isConnectable={false}
        className="map-port map-port-in"
      />
      <Handle
        type="source"
        position={Position.Right}
        id="out"
        isConnectable={false}
        className="map-port map-port-off"
      />
      <div className="flex h-10 items-center gap-2 px-4">
        <ProductLogo
          id="docker-compose"
          size="sm"
          className="size-6 rounded-md"
          fallback={Layers}
        />
        <span className="eyebrow shrink-0">Stack</span>
        <span className="min-w-0 truncate font-mono text-hint font-medium text-foreground">
          {item.name}
        </span>
        <Status
          tone={TONE_DOT[item.tone]}
          label={item.state}
          className="ml-auto max-w-[45%] min-w-0 text-micro [&>span:last-child]:truncate"
        />
      </div>
    </div>
  )
})

export const nodeTypes = {
  hero: HeroNode,
  stack: StackNode,
  entry: EntryNode,
  runtime: RuntimeNode,
  process: ProcessNode,
  datastore: DatastoreNode,
  volume: VolumeNode,
  backup: BackupNode,
  satellite: SatelliteNode,
  systemFrame: SystemFrameNode,
  stackFrame: StackFrameNode,
}

/** The inspector's head and the canvas's label for a node: what kind of thing it is. */
export function nodeKindLabel(node: SystemNode) {
  if (node.kind === "containers") return `${ROLE_LABEL[node.role ?? "app"]} container`
  return kindLabel(node.kind, node.itemId)
}
