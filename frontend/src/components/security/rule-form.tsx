"use client"

import { useMemo, useState } from "react"
import { ArrowUpDown, Check, Plus, Shield, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AppProfile, FirewallRule, ServicePreset } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

/**
 * Opening a port, for somebody who does not already know the numbers.
 *
 * The old form was an action, a port box and a source box — which assumes the
 * reader knows that Redis is 6379 and, more importantly, that opening it is
 * the same as handing over the machine. The catalogue comes from the server so
 * the names and the warnings are the same list the security audit reads, and
 * the warning appears at the moment of choosing rather than in a report
 * afterwards.
 *
 * Application profiles are the other half: a rule written as "Nginx Full" is
 * the host's own package speaking, and it keeps meaning what it says if that
 * package later adds a port.
 */
export function AddRuleDialog({
  onDone,
  hasProfiles = true,
}: {
  onDone: () => void
  hasProfiles?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <Plus className="size-4" />
        Add rule
      </Button>
      {/* Keyed so a second visit never opens with the previous rule still in
          the boxes — an almost-right rule is worse than a blank one. */}
      {open && (
        <RuleForm
          key="rule-form"
          open={open}
          onOpenChange={setOpen}
          hasProfiles={hasProfiles}
          onDone={() => {
            setOpen(false)
            onDone()
          }}
        />
      )}
    </>
  )
}

/**
 * The same form, opened on a rule that already exists.
 *
 * A firewall has no edit — a rule is a line, and changing one means writing
 * another and removing this one — but "delete it and get it right the second
 * time" is how a port ends up open for the thirty seconds in between, or shut
 * permanently because the retype went wrong. The server does the replacement
 * in the safe order; this is the form that asks for it.
 */
export function EditRuleDialog({
  rule,
  open,
  onOpenChange,
  onDone,
  hasProfiles = true,
}: {
  rule: FirewallRule
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  hasProfiles?: boolean
}) {
  if (!open) return null
  return (
    <RuleForm
      key={`edit-${rule.number}`}
      open={open}
      onOpenChange={onOpenChange}
      hasProfiles={hasProfiles}
      edit={rule}
      onDone={() => {
        onOpenChange(false)
        onDone()
      }}
    />
  )
}

const SOURCE_PRESETS = [
  { key: "anywhere", label: "Anywhere", value: "", hint: "Every address on the internet" },
  {
    key: "private",
    label: "Private network",
    value: "10.0.0.0/8",
    hint: "RFC1918 — adjust to your range",
  },
  { key: "tailnet", label: "Tailnet", value: "100.64.0.0/10", hint: "Tailscale's address range" },
  { key: "custom", label: "Specific address", value: "", hint: "One IP or a CIDR" },
] as const

/**
 * A listed rule read back into the form's fields.
 *
 * ufw prints "Anywhere" where the request that made the rule said nothing at
 * all, so the round trip has to undo that or every edit would come back with a
 * literal source of "Anywhere" and be refused as not an address. A rule with
 * no port was written from an application profile, and its target is that
 * profile's name — except when the destination is "Anywhere", which is what a
 * source-only rule (`deny from 203.0.113.9`) prints there. Read as a profile
 * name that becomes `app Anywhere`, which is not a profile on any host.
 */
function fieldsOf(rule?: FirewallRule) {
  const from = rule?.from ?? ""
  const anywhere = from === "" || /^anywhere/i.test(from)
  const to = rule?.to ?? ""
  const profile = rule && !rule.port && to && !/^\d/.test(to) && !/^anywhere/i.test(to) ? to : ""
  return {
    action: (rule?.action ?? "allow").toLowerCase(),
    direction: (rule?.direction ?? "in").toLowerCase() === "out" ? "out" : "in",
    mode: (profile ? "profile" : "service") as "service" | "profile",
    profile,
    port: rule?.port ?? "",
    protocol: rule?.protocol || "tcp",
    sourceKind: anywhere ? "anywhere" : "custom",
    from: anywhere ? "" : from,
    comment: rule?.comment ?? "",
  }
}

function RuleForm({
  open,
  onOpenChange,
  onDone,
  hasProfiles,
  edit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  hasProfiles: boolean
  edit?: FirewallRule
}) {
  const initial = useMemo(() => fieldsOf(edit), [edit])
  const [action, setAction] = useState(initial.action)
  const [direction, setDirection] = useState(initial.direction)
  const [position, setPosition] = useState("")
  const [mode, setMode] = useState<"service" | "profile">(initial.mode)
  const [preset, setPreset] = useState("")
  const [profile, setProfile] = useState(initial.profile)
  const [port, setPort] = useState(initial.port)
  const [protocol, setProtocol] = useState(initial.protocol)
  const [sourceKind, setSourceKind] = useState<string>(initial.sourceKind)
  const [from, setFrom] = useState(initial.from)
  const [comment, setComment] = useState(initial.comment)
  const [busy, setBusy] = useState(false)

  const services = usePoll<ServicePreset[]>(
    (signal) => get("/security/services", undefined, signal),
    0,
  )
  const profiles = usePoll<AppProfile[]>(
    (signal) => get("/firewall/apps", undefined, signal),
    0,
    [],
    {
      enabled: hasProfiles,
    },
  )

  const chosen = useMemo(
    () => services.data?.find((s) => s.key === preset),
    [services.data, preset],
  )
  // The warning has to follow the port, not the dropdown. Picking Redis from
  // the list and typing 6379 are the same rule, and opening an existing rule
  // that already exposes it is the moment somebody is best placed to fix it —
  // which is exactly the case a warning keyed to the select never fires in.
  const matched = useMemo(
    () => chosen ?? services.data?.find((s) => s.port === port && s.protocol === protocol),
    [chosen, services.data, port, protocol],
  )
  const source =
    sourceKind === "custom" ? from : (SOURCE_PRESETS.find((p) => p.key === sourceKind)?.value ?? "")
  const unrestricted = source === ""
  const dangerous = action === "allow" && unrestricted && !!matched?.danger

  const applyPreset = (key: string) => {
    setPreset(key)
    const found = services.data?.find((s) => s.key === key)
    if (found) {
      setPort(found.port)
      setProtocol(found.protocol)
      if (!comment) setComment(found.name)
    }
  }

  const submit = async () => {
    setBusy(true)
    const body = {
      action,
      direction,
      port: mode === "service" ? port : "",
      protocol: mode === "service" ? protocol : "",
      app: mode === "profile" ? profile : "",
      from: source,
      comment,
      // An edit keeps the rule's place: the server inserts the replacement
      // where the original was, which is the only way a firewall whose first
      // match wins can be edited without changing what it does.
      position: edit ? 0 : Number(position) || 0,
    }
    try {
      if (edit?.number !== undefined) {
        await put(`/firewall/rules/${edit.number}`, body)
        notify.success("Rule replaced")
      } else {
        await post("/firewall/rules", body)
        notify.success("Rule added")
      }
      onDone()
    } catch (err) {
      notify.error("Rule rejected", err)
    } finally {
      setBusy(false)
    }
  }

  const ready = mode === "service" ? port !== "" : profile !== ""

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      icon={Shield}
      title={edit ? `Edit rule ${edit.number}` : "New inbound rule"}
      description={
        edit
          ? "A firewall has no edit, so this writes the replacement first and removes the original once it is in — the rule keeps its place and the port is never briefly unprotected."
          : "Rules are checked in order and the first match wins. A rule that would block the address you are connected from is refused before it is applied."
      }
      footer={
        <>
          {/* The command sits on the left and wraps: a long `ufw` line in a
              row that cannot shrink would push the footer wider than the
              dialog and clip the button on the right. */}
          <code
            className={cn(
              "mr-auto min-w-0 rounded-md border border-hairline px-2 py-1 font-mono text-hint leading-relaxed break-words text-muted-foreground",
              !ready && "opacity-0",
            )}
          >
            ufw {edit ? `insert ${edit.number} ` : position ? `insert ${position} ` : ""}
            {action} {direction}
            {source && ` from ${source}`}
            {mode === "service"
              ? ` to any port ${port}${protocol ? ` proto ${protocol}` : ""}`
              : ` app ${profile}`}
          </code>
          <Button onClick={submit} disabled={!ready || busy}>
            {edit ? "Save changes" : "Add rule"}
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <div className="grid gap-3 sm:grid-cols-[1fr_9rem]">
          <div className="space-y-1.5">
            <Label>Action</Label>
            <Select value={action} onValueChange={setAction}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="allow">allow — let it through</SelectItem>
                <SelectItem value="limit">
                  limit — allow, but rate-limit repeat connections
                </SelectItem>
                <SelectItem value="deny">deny — drop silently</SelectItem>
                <SelectItem value="reject">reject — refuse and say so</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>Direction</Label>
            <Select value={direction} onValueChange={setDirection}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="in">inbound</SelectItem>
                <SelectItem value="out">outbound</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        {direction === "out" && (
          <p className="-mt-1 text-hint leading-relaxed text-muted-foreground">
            Outbound rules govern what this server may reach, not who may reach it. Restricting
            egress breaks package updates and certificate renewal unless you allow them first.
          </p>
        )}

        <Tabs value={mode} onValueChange={(v) => setMode(v as "service" | "profile")}>
          <TabsList className="w-full">
            <TabsTrigger value="service" className="flex-1">
              Port
            </TabsTrigger>
            <TabsTrigger
              value="profile"
              className="flex-1"
              disabled={!hasProfiles || !profiles.data?.length}
            >
              Application profile
            </TabsTrigger>
          </TabsList>

          <TabsContent value="service" className="mt-3 space-y-3">
            <div className="space-y-1.5">
              <Label>Service</Label>
              <Select value={preset} onValueChange={applyPreset}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Pick one, or type a port below" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectLabel>Common</SelectLabel>
                    {services.data
                      ?.filter((s) => !s.danger)
                      .map((s) => (
                        <SelectItem key={s.key} value={s.key}>
                          {s.name} · {s.port}/{s.protocol}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                  <SelectGroup>
                    <SelectLabel>Keep these off the internet</SelectLabel>
                    {services.data
                      ?.filter((s) => s.danger)
                      .map((s) => (
                        <SelectItem key={s.key} value={s.key}>
                          {s.name} · {s.port}/{s.protocol}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              {chosen && <p className="text-hint text-muted-foreground">{chosen.detail}</p>}
            </div>

            <div className="grid gap-3 sm:grid-cols-[1fr_8rem]">
              <div className="space-y-1.5">
                <Label htmlFor="rule-port">Port, range or list</Label>
                <Input
                  id="rule-port"
                  value={port}
                  onChange={(e) => {
                    setPort(e.target.value)
                    setPreset("")
                  }}
                  placeholder="443, 8000:8010 or 80,443"
                />
              </div>
              <div className="space-y-1.5">
                <Label>Protocol</Label>
                <Select value={protocol} onValueChange={setProtocol}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tcp">tcp</SelectItem>
                    <SelectItem value="udp">udp</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </TabsContent>

          <TabsContent value="profile" className="mt-3 space-y-1.5">
            <Label>Profile</Label>
            {/* Searchable rather than a plain select: ufw defines a handful of
                profiles and firewalld defines several hundred, and the same
                control has to be usable for both. */}
            <ProfilePicker profiles={profiles.data ?? []} value={profile} onChange={setProfile} />
            <p className="text-hint text-muted-foreground">
              A profile names its own ports, so the rule keeps meaning what it says if the package
              later adds one.
            </p>
          </TabsContent>
        </Tabs>

        <div className="space-y-1.5">
          <Label>Source</Label>
          <div className="flex flex-wrap gap-1.5">
            {SOURCE_PRESETS.map((p) => (
              <Button
                key={p.key}
                type="button"
                size="xs"
                variant={sourceKind === p.key ? "default" : "outline"}
                onClick={() => {
                  setSourceKind(p.key)
                  setFrom(p.value)
                }}
              >
                {p.label}
              </Button>
            ))}
          </div>
          {sourceKind === "custom" || sourceKind === "private" || sourceKind === "tailnet" ? (
            <Input
              value={sourceKind === "custom" ? from : source}
              onChange={(e) => {
                setSourceKind("custom")
                setFrom(e.target.value)
              }}
              placeholder="10.0.0.0/8 or 203.0.113.9"
              className="font-mono text-xs"
            />
          ) : (
            <p className="text-hint text-muted-foreground">
              {SOURCE_PRESETS.find((p) => p.key === sourceKind)?.hint}
            </p>
          )}
        </div>

        <div className={cn("space-y-1.5", edit && "hidden")}>
          <Label htmlFor="rule-position">Insert at</Label>
          <Input
            id="rule-position"
            value={position}
            inputMode="numeric"
            onChange={(e) => setPosition(e.target.value)}
            placeholder="leave empty to add at the end"
          />
          <p className="text-hint leading-relaxed text-muted-foreground">
            Rules are checked in order and the first match wins, so a deny added after a broad allow
            does nothing at all — which looks exactly like a deny that works. Give a number to put
            this one in front.
          </p>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="rule-comment">Comment</Label>
          <Input
            id="rule-comment"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="What this is for"
          />
        </div>

        {dangerous && (
          <Notice tone="danger" icon={Warning} title={`${matched?.name} open to the internet`}>
            {matched?.danger} Choose a source above, or bind the service to 127.0.0.1 instead of
            opening the port at all.
          </Notice>
        )}
      </div>
    </Modal>
  )
}

/**
 * A searchable picker for the host's service profiles.
 *
 * ufw defines about a dozen; firewalld ships several hundred. One dropdown has
 * to work for both, and a plain select at that size is a scroll bar and a
 * guess.
 */
function ProfilePicker({
  profiles,
  value,
  onChange,
}: {
  profiles: AppProfile[]
  value: string
  onChange: (value: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-expanded={open}
          className="w-full justify-between font-normal"
        >
          {value || (
            <span className="text-muted-foreground">Defined by the host&rsquo;s packages</span>
          )}
          <ArrowUpDown className="size-3.5 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
        <Command>
          <CommandInput placeholder="Search profiles…" className="h-9" />
          <CommandList>
            <CommandEmpty>Nothing matches.</CommandEmpty>
            <CommandGroup>
              {profiles.map((p) => (
                <CommandItem
                  key={p.name}
                  value={p.name}
                  onSelect={() => {
                    onChange(p.name)
                    setOpen(false)
                  }}
                >
                  <Check
                    className={cn("size-3.5", value === p.name ? "opacity-100" : "opacity-0")}
                  />
                  <span className="flex-1 truncate">{p.name}</span>
                  {p.ports.length > 0 && (
                    <span className="font-mono text-micro text-muted-foreground">
                      {p.ports.join(" ")}
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
