"use client"

import { useMemo, useRef, useState } from "react"
import { Check, Copy, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import type { NetworkOverview, VPNView } from "@/lib/types"
import { Field, FieldRow, FormSection, InfoTip } from "@/components/form"
import { Well } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { useSecurity } from "@/components/security/security-context"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/**
 * The five directives that decide whether this server can be somebody's
 * jump host. They are drawn here, under the preset that sets four of them,
 * and are left out of the Settings list so each is on the page once.
 */
export const JUMP_KEYS = [
  "allowtcpforwarding",
  "allowagentforwarding",
  "gatewayports",
  "permittunnel",
  "maxsessions",
]

/**
 * The profile of a bastion: it forwards TCP, which is all `ssh -J` needs, and
 * closes everything else a machine that only relays connections has no use
 * for. Agent forwarding is the one that matters most — with it on, root on
 * the bastion can use the agent of everyone who passes through — and the
 * other two stop it becoming a way onto the network of its own. MaxSessions
 * is left alone: it is a capacity, not a posture.
 */
export const BASTION_PROFILE: Record<string, string> = {
  allowtcpforwarding: "yes",
  allowagentforwarding: "no",
  gatewayports: "no",
  permittunnel: "no",
}

/** Forwarding values under which sshd opens the direct-tcpip channel `-J` rides on. */
const JUMPS = ["yes", "local", "all"]

type Suggestion = {
  key: string
  group: "WireGuard" | "Tailscale" | "Docker"
  label: string
  /** The address a host is reached at, or nothing for a network the reader completes. */
  address?: string
  /** What goes in the typed field when the suggestion is a network, not a host. */
  stem?: string
  hint?: string
}

const GROUP_PRODUCT = { WireGuard: "wireguard", Tailscale: "tailscale", Docker: "docker" } as const

/** Past this many a group is a count: a tailnet of sixty machines is not a row of chips. */
const PER_GROUP = 8

const HOST = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/
const USER = /^[A-Za-z_][A-Za-z0-9._-]*$/

/**
 * Using this server as a jump host: the preset that makes it one, and the
 * `~/.ssh/config` block and `ssh -J` line that go through it.
 *
 * The settings are the five directives (`rows`, drawn by the SSH page's own
 * setting rows so they stage into the same draft and apply bar as every other
 * directive) and the preset only writes into that draft: nothing is changed
 * until Test and apply is pressed. Beside them, the generator. Hosts behind
 * this one are suggested from what the dashboard already knows — WireGuard
 * peers, tailnet machines, the subnets of Docker networks — or typed; an
 * address that comes from typing is checked before it is put into a snippet
 * somebody will paste into their shell config.
 *
 * What it says about whether jumping works follows the draft, and notes when
 * the saved value disagrees: "allowed" while the file still says no is a
 * snippet that will fail until the change is applied.
 */
export function JumpHost({
  value,
  saved,
  edited,
  port,
  user,
  rows,
  onPreset,
}: {
  /** A directive's value as the page holds it — the draft over the saved one. */
  value: (key: string) => string | undefined
  /** AllowTcpForwarding as sshd reports it now. */
  saved?: string
  edited: boolean
  port: string
  /** An account that holds a key here: the likeliest user to jump as. */
  user: string
  rows: React.ReactNode
  onPreset: () => void
}) {
  const forwarding = value("allowtcpforwarding") ?? "yes"
  const allowed = JUMPS.includes(forwarding)
  const profiled = Object.entries(BASTION_PROFILE).every(([key, want]) => value(key) === want)

  return (
    <div className="grid min-w-0 gap-x-12 gap-y-12 pt-6 xl:grid-cols-2">
      <FormSection
        aside
        title="Forwarding"
        className="max-w-none py-0"
        actions={
          <>
            {edited && <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>}
            <InfoTip label="What the profile sets">
              Allows TCP forwarding, which is what <span className="font-mono">ssh -J</span> rides
              on, and refuses agent forwarding, gateway ports and tunnel devices, none of which a
              machine that only relays connections needs. Staged here; nothing changes until Test
              and apply.
            </InfoTip>
            <Button size="xs" variant="outline" onClick={onPreset} disabled={profiled}>
              Use it as a jump host
            </Button>
          </>
        }
        hint={
          <Status
            tone={allowed ? "running" : forwarding === "remote" ? "notice" : "stopped"}
            label={
              allowed
                ? "Other hosts can be reached through it"
                : forwarding === "remote"
                  ? "Remote forwards only — jumping is refused"
                  : "Forwarding is refused — jumping is refused"
            }
          />
        }
      >
        <div className="divide-y divide-hairline">{rows}</div>
      </FormSection>

      <FormSection aside title="Connect through it" className="max-w-none py-0">
        <Generator
          allowed={allowed}
          applied={saved === undefined || JUMPS.includes(saved)}
          port={port}
          user={user}
        />
      </FormSection>
    </div>
  )
}

function Generator({
  allowed,
  applied,
  port,
  user,
}: {
  allowed: boolean
  /** The file already says yes: the block works the moment it is pasted. */
  applied: boolean
  port: string
  user: string
}) {
  const { exposure } = useSecurity()
  // Both reads are an administrator's and either can fail on a host without the
  // product behind it; the suggestions are an offer and the field always works.
  const vpn = usePoll<VPNView>((signal) => get("/network/vpn", undefined, signal), 0)
  const overview = usePoll<NetworkOverview>(
    (signal) => get("/network/overview", undefined, signal),
    0,
  )

  const [address, setAddress] = useState<string>()
  const [jumpUser, setJumpUser] = useState<string>()
  const [targetUser, setTargetUser] = useState<string>()
  const [targets, setTargets] = useState<{ address: string; alias: string }[]>([])
  const [typed, setTyped] = useState("")
  const [typedError, setTypedError] = useState<string>()
  const typedInput = useRef<HTMLInputElement>(null)

  // Where this server is reached from the reader's side: its tailnet address
  // when it has one, which is how the dashboard itself is usually reached, and
  // otherwise the name this very page was opened on.
  const here = address ?? exposure?.tailscaleIp ?? window.location.hostname
  const jump = jumpUser ?? user
  const as = targetUser ?? jump
  const suggestions = useMemo(() => suggest(vpn.data, overview.data), [vpn.data, overview.data])

  const valid = {
    here: HOST.test(here),
    jump: USER.test(jump),
    as: USER.test(as),
  }
  const ready = valid.here && valid.jump && valid.as
  const via = `${jump}@${here}${port !== "22" ? `:${port}` : ""}`

  const picked = (candidate: string) => targets.some((t) => t.address === candidate)
  const toggle = (candidate: string, alias: string) =>
    setTargets((now) =>
      picked(candidate)
        ? now.filter((t) => t.address !== candidate)
        : [...now, { address: candidate, alias: aliasOf(alias) }],
    )
  const add = () => {
    const candidate = typed.trim()
    if (!candidate) return
    if (!HOST.test(candidate)) {
      setTypedError("A host name or an address, with no spaces")
      return
    }
    setTypedError(undefined)
    if (!picked(candidate)) toggle(candidate, candidate)
    setTyped("")
  }

  const block = targets
    .map((t) => `Host ${t.alias}\n  HostName ${t.address}\n  User ${as}\n  ProxyJump ${via}`)
    .join("\n\n")

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <FieldRow>
        <Field
          label="This server"
          htmlFor="jump-address"
          hint="Where you reach it from"
          error={valid.here ? undefined : "Not a host name or address"}
        >
          <Input
            id="jump-address"
            value={here}
            onChange={(event) => setAddress(event.target.value.trim())}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        <Field
          label="Your user here"
          htmlFor="jump-user"
          hint={user === jump ? "An account with a key" : undefined}
          error={valid.jump ? undefined : "Letters, digits, dot, dash and underscore"}
        >
          <Input
            id="jump-user"
            value={jump}
            onChange={(event) => setJumpUser(event.target.value.trim())}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      </FieldRow>

      <div className="space-y-3">
        <p className="text-body font-medium">Hosts behind it</p>
        {(["WireGuard", "Tailscale", "Docker"] as const).map((group) => {
          const items = suggestions.filter((s) => s.group === group)
          if (items.length === 0) return null
          return (
            <div key={group} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
              <span className="inline-flex w-24 shrink-0 items-center gap-1.5 text-hint text-muted-foreground">
                <ProductGlyph id={GROUP_PRODUCT[group]} />
                {group}
              </span>
              <ChipStrip aria-label={`${group} hosts`} className="min-w-0 flex-1">
                {items.slice(0, PER_GROUP).map((s) => (
                  <FilterChip
                    key={s.key}
                    selected={s.address ? picked(s.address) : false}
                    title={s.hint}
                    onClick={() => {
                      if (s.address) return toggle(s.address, s.label)
                      setTyped(s.stem ?? "")
                      typedInput.current?.focus()
                    }}
                  >
                    {s.label}
                    {s.address && (
                      <span className="font-mono font-normal opacity-60">{s.address}</span>
                    )}
                  </FilterChip>
                ))}
                {items.length > PER_GROUP && (
                  <span className="numeric px-1 text-hint text-muted-foreground">
                    +{items.length - PER_GROUP} more, typed below
                  </span>
                )}
              </ChipStrip>
            </div>
          )
        })}
        <Field label="Another host" htmlFor="jump-target" error={typedError}>
          <div className="flex gap-2">
            <Input
              id="jump-target"
              ref={typedInput}
              value={typed}
              onChange={(event) => {
                setTyped(event.target.value)
                setTypedError(undefined)
              }}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return
                event.preventDefault()
                add()
              }}
              placeholder="10.0.0.5 or db.internal"
              className="min-w-0 flex-1 font-mono"
              autoComplete="off"
              spellCheck={false}
            />
            <Button variant="outline" onClick={add} disabled={!typed.trim()}>
              <Plus aria-hidden />
              Add
            </Button>
          </div>
        </Field>
        {targets.length > 0 && (
          <ChipStrip aria-label="Hosts in the snippet">
            {targets.map((t) => (
              <FilterChip key={t.address} selected onClick={() => toggle(t.address, t.alias)}>
                <span className="font-mono">{t.address}</span>
                <span className="sr-only">, remove</span>
                <span aria-hidden className="opacity-60">
                  ×
                </span>
              </FilterChip>
            ))}
          </ChipStrip>
        )}
      </div>

      <Field
        label="User on those hosts"
        htmlFor="jump-as"
        error={valid.as ? undefined : "Letters, digits, dot, dash and underscore"}
      >
        <Input
          id="jump-as"
          value={as}
          onChange={(event) => setTargetUser(event.target.value.trim())}
          className="max-w-60 font-mono"
          autoComplete="off"
          spellCheck={false}
        />
      </Field>

      {!allowed ? (
        <Notice tone="warning" title="Jumping through this server is refused">
          sshd closes the channel <span className="font-mono">ssh -J</span> opens while
          AllowTcpForwarding is not yes, local or all. Allow it above, or use the preset, and apply.
        </Notice>
      ) : targets.length === 0 ? (
        <p className="text-hint text-muted-foreground">
          Pick or type a host and its block appears here.
        </p>
      ) : !ready ? null : (
        <div className="flex min-w-0 flex-col gap-4">
          {!applied && (
            <p className="text-hint text-warning">
              The change that allows this is staged. It works once it is applied.
            </p>
          )}
          <Snippet label="~/.ssh/config block" caption="~/.ssh/config" text={block} />
          {targets.map((t) => (
            <Snippet
              key={t.address}
              label={`ssh command for ${t.address}`}
              caption={t.address === targets[0].address ? "or once, from a terminal" : undefined}
              text={`ssh -J ${via} ${as}@${t.address}`}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/** A recessed block of text with the one control that matters: copy it. */
function Snippet({ label, caption, text }: { label: string; caption?: string; text: string }) {
  const { copied, copy } = useCopy()
  return (
    <div className="min-w-0 space-y-1.5">
      {caption && <p className="font-mono text-hint text-muted-foreground">{caption}</p>}
      <Well className="relative pr-11">
        <pre data-slot="snippet" className="break-all whitespace-pre-wrap">
          {text}
        </pre>
        <Button
          variant="ghost"
          size="icon-xs"
          className="absolute top-2 right-2"
          aria-label={`Copy the ${label}`}
          onClick={() => void copy(text)}
        >
          {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        </Button>
      </Well>
    </div>
  )
}

/** A Host alias ssh will accept: what a person types after `ssh `. */
function aliasOf(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9._-]+/g, "-")
}

/**
 * What the dashboard already knows is behind this server, as an offer: a
 * WireGuard peer's own address (not a site's whole network), a tailnet
 * machine's first IPv4, and — since a container's address is not read — the
 * start of each Docker subnet for the reader to finish.
 */
function suggest(vpn?: VPNView, overview?: NetworkOverview): Suggestion[] {
  const found: Suggestion[] = []
  for (const tunnel of vpn?.wireguard.interfaces ?? []) {
    for (const peer of tunnel.peers) {
      const [base, bits] = peer.address.split("/")
      if (!base || (bits !== undefined && bits !== "32")) continue
      found.push({
        key: `wg:${tunnel.name}:${peer.publicKey}`,
        group: "WireGuard",
        label: peer.name || base,
        address: base,
        hint: `${tunnel.name}${peer.online ? ", connected" : ""}`,
      })
    }
  }
  const tailnet = [...(vpn?.tailscale.peers ?? [])].sort(
    (a, b) => Number(b.online) - Number(a.online),
  )
  for (const peer of tailnet) {
    const v4 = peer.tailscaleIps.find((ip) => !ip.includes(":"))
    if (!v4) continue
    found.push({
      key: `ts:${peer.id}`,
      group: "Tailscale",
      label: peer.hostName,
      address: v4,
      hint: peer.online ? "online" : "offline",
    })
  }
  for (const network of overview?.dockerNetworks ?? []) {
    for (const subnet of network.subnets) {
      const [base, bits] = subnet.split("/")
      if (!base.includes(".")) continue
      const keep = Math.max(1, Math.min(3, Math.floor(Number(bits) / 8)))
      found.push({
        key: `docker:${network.id}:${subnet}`,
        group: "Docker",
        label: network.name,
        stem: `${base.split(".").slice(0, keep).join(".")}.`,
        hint: `${subnet}, ${network.containers.length} containers — type the host`,
      })
    }
  }
  return found
}
