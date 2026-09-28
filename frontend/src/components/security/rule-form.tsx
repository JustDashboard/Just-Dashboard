"use client"

import { useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { ArrowUpDown, Check, Globe, NetworkDevice, Plus, Router, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AppProfile, FirewallRule, ServicePreset } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Field, FieldRow, FormSection, Disclosure } from "@/components/form"
import { ChoiceGrid, ChoiceCard } from "@/components/choice-card"
import { ProductGlyph, ProductLogo, portProduct } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
  arrival: given,
}: {
  onDone: () => void
  hasProfiles?: boolean
  /** A rule handed over by a link (`?add=1&port=…`): the dialog opens on it, unsent. */
  arrival?: RuleArrival
}) {
  // Used for the one opening the link asked for; the next "Add rule" is blank.
  const [arrival, setArrival] = useState(given)
  const [open, setOpen] = useSessionState(
    "security.firewall.adding",
    false,
    arrival ? true : undefined,
  )
  // Forgotten on close — a second opening never has the previous rule still
  // in the boxes, since an almost-right rule is worse than a blank one — and
  // kept while open, so a look at the ports page comes back to the same rule.
  const close = () => {
    setOpen(false)
    setArrival(undefined)
    forgetSessionState("security.firewall.rule.")
  }
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus className="size-4" />
        Add rule
      </Button>
      {open && (
        <RuleForm
          key="rule-form"
          open={open}
          onOpenChange={(next) => !next && close()}
          hasProfiles={hasProfiles}
          arrival={arrival}
          onDone={() => {
            close()
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
  arrival,
}: {
  rule: FirewallRule
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  hasProfiles?: boolean
  /** The change a link (`?edit=3&source=tailnet`) asked for, laid over the rule. */
  arrival?: RuleArrival
}) {
  if (!open) return null
  return (
    <RuleForm
      key={`edit-${rule.number}`}
      open={open}
      onOpenChange={onOpenChange}
      hasProfiles={hasProfiles}
      edit={rule}
      arrival={arrival}
      onDone={() => {
        onOpenChange(false)
        onDone()
      }}
    />
  )
}

/**
 * Fields a link fills in, as the form holds them. Each one given replaces
 * what the form would have opened on; nothing is submitted until the
 * operator presses the button.
 */
export type RuleArrival = Partial<{
  action: string
  port: string
  protocol: string
  sourceKind: string
  from: string
  comment: string
  position: string
}>

/** What a link into the firewall page asks its dialogs to open on. */
export type RuleHandoff = {
  add?: RuleArrival
  /** Rule `number`, opened only while it is still the rule for `port`. */
  edit?: { number: number; port: string; fields: RuleArrival }
}

const HANDOFF_ACTIONS = ["allow", "limit", "deny", "reject"]

/**
 * Reads `?add=1&port=&proto=&action=&source=&comment=&position=` or
 * `?edit=N&port=&action=&source=`. A value the form could not hold is
 * dropped rather than passed on, so a bad link opens a blank field, never a
 * rule it did not mean. `source` is `tailnet`, `anywhere` or an address.
 */
export function handoffFromParams(params: URLSearchParams): RuleHandoff | undefined {
  const port = params.get("port") ?? ""
  if (!/^\d{1,5}$/.test(port)) return undefined
  const fields: RuleArrival = {}
  const action = params.get("action")
  if (action && HANDOFF_ACTIONS.includes(action)) fields.action = action
  const source = params.get("source")
  if (source === "tailnet" || source === "anywhere") {
    fields.sourceKind = source
    fields.from = ""
  } else if (source && /^[0-9a-fA-F.:/]+$/.test(source)) {
    fields.sourceKind = "custom"
    fields.from = source
  }
  const edit = params.get("edit")
  if (edit && /^\d{1,4}$/.test(edit)) return { edit: { number: Number(edit), port, fields } }
  if (params.get("add") !== "1") return undefined
  // Every field is given, so a draft left open in this tab cannot lend the
  // new rule a source or a position the link never asked for.
  const proto = params.get("proto")
  const position = params.get("position") ?? ""
  return {
    add: {
      action: "allow",
      sourceKind: "anywhere",
      from: "",
      ...fields,
      port,
      protocol: proto === "udp" ? "udp" : "tcp",
      comment: (params.get("comment") ?? "").slice(0, 64),
      position: /^\d{1,4}$/.test(position) ? position : "",
    },
  }
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
  arrival,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  hasProfiles: boolean
  edit?: FirewallRule
  arrival?: RuleArrival
}) {
  const initial = useMemo(() => fieldsOf(edit), [edit])
  // Kept for the tab while the dialog is open; whoever opened it forgets it on close.
  const draft = `security.firewall.rule.${edit?.number ?? "new"}`
  const [action, setAction] = useSessionState(`${draft}.action`, initial.action, arrival?.action)
  const [direction, setDirection] = useSessionState(`${draft}.direction`, initial.direction)
  const [position, setPosition] = useSessionState(`${draft}.position`, "", arrival?.position)
  const [mode, setMode] = useSessionState<"service" | "profile">(`${draft}.mode`, initial.mode)
  const [preset, setPreset] = useSessionState(`${draft}.preset`, "")
  const [profile, setProfile] = useSessionState(`${draft}.profile`, initial.profile)
  const [port, setPort] = useSessionState(`${draft}.port`, initial.port, arrival?.port)
  const [protocol, setProtocol] = useSessionState(
    `${draft}.protocol`,
    initial.protocol,
    arrival?.protocol,
  )
  const [sourceKind, setSourceKind] = useSessionState<string>(
    `${draft}.sourceKind`,
    initial.sourceKind,
    arrival?.sourceKind,
  )
  const [from, setFrom] = useSessionState(`${draft}.from`, initial.from, arrival?.from)
  const [comment, setComment] = useSessionState(
    `${draft}.comment`,
    initial.comment,
    arrival?.comment,
  )
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

  const ready =
    mode === "service"
      ? Boolean(port.trim() || (source.trim() && ["deny", "reject"].includes(action)))
      : profile !== ""

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={edit ? `Edit rule ${edit.number}` : "New firewall rule"}
      size="lg"
      description={
        edit
          ? "A firewall has no edit, so this writes the replacement first and removes the original once it is in — the rule keeps its place and the port is never briefly unprotected."
          : "Rules are checked in order and the first match wins. A rule that would block the address you are connected from is refused before it is applied."
      }
      footer={
        <>
          {/* Long source ranges must wrap without pushing the submit button out of the dialog. */}
          <code
            className={cn(
              "mr-auto min-w-0 rounded-md border border-hairline px-2 py-1 font-mono text-hint leading-relaxed break-words text-muted-foreground",
              !ready && "opacity-0",
            )}
          >
            {edit ? `Rule ${edit.number}: ` : position ? `Position ${position}: ` : ""}
            {action} {direction}
            {source && ` from ${source}`}
            {" to any"}
            {mode === "service"
              ? port && ` port ${port}${protocol ? ` proto ${protocol}` : ""}`
              : ` app ${profile}`}
          </code>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            {edit ? "Save changes" : "Add rule"}
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <FormSection title="Traffic policy">
          <FieldRow>
            <Field label="Action" htmlFor="rule-action">
              <Select value={action} onValueChange={setAction}>
                <SelectTrigger id="rule-action" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="allow">Allow</SelectItem>
                  <SelectItem value="limit">Allow with rate limit</SelectItem>
                  <SelectItem value="deny">Deny silently</SelectItem>
                  <SelectItem value="reject">Reject with a response</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Direction" htmlFor="rule-direction">
              <Select value={direction} onValueChange={setDirection}>
                <SelectTrigger id="rule-direction" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="in">Inbound</SelectItem>
                  <SelectItem value="out">Outbound</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
          {direction === "out" && (
            <p className="text-hint text-warning">
              Outbound restrictions can interrupt package updates and certificate renewal.
            </p>
          )}
        </FormSection>

        <FormSection title="Destination">
          <Tabs value={mode} onValueChange={(value) => setMode(value as "service" | "profile")}>
            <TabsList>
              <TabsTrigger value="service">Port or service</TabsTrigger>
              <TabsTrigger value="profile" disabled={!hasProfiles || !profiles.data?.length}>
                Application profile
              </TabsTrigger>
            </TabsList>
            <TabsContent value="service" className="space-y-4 pt-3">
              <Field label="Service" htmlFor="rule-service" hint={chosen?.detail}>
                <Select value={preset} onValueChange={applyPreset}>
                  <SelectTrigger id="rule-service" className="w-full">
                    <SelectValue placeholder="Choose a service, or enter a port" />
                  </SelectTrigger>
                  <SelectContent>
                    {[false, true].map((danger) => (
                      <SelectGroup key={String(danger)}>
                        <SelectLabel>
                          {danger ? "Keep off the internet" : "Common services"}
                        </SelectLabel>
                        {services.data
                          ?.filter((service) => Boolean(service.danger) === danger)
                          .map((service) => (
                            <SelectItem key={service.key} value={service.key}>
                              {portProduct(Number(service.port)) && (
                                <ProductGlyph id={portProduct(Number(service.port))!} />
                              )}
                              {service.name} · {service.port}/{service.protocol}
                            </SelectItem>
                          ))}
                      </SelectGroup>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <FieldRow>
                <Field
                  label="Port, range or list"
                  htmlFor="rule-port"
                  hint="Leave empty for an address-only deny rule."
                >
                  <Input
                    id="rule-port"
                    value={port}
                    onChange={(event) => {
                      setPort(event.target.value)
                      setPreset("")
                    }}
                    placeholder="443, 8000:8010 or 80,443"
                    className="font-mono"
                  />
                </Field>
                <Field label="Protocol" htmlFor="rule-protocol">
                  <Select value={protocol} onValueChange={setProtocol}>
                    <SelectTrigger id="rule-protocol" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="tcp">TCP</SelectItem>
                      <SelectItem value="udp">UDP</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
              </FieldRow>
            </TabsContent>
            <TabsContent value="profile" className="pt-3">
              <Field
                label="Application profile"
                hint="The host's profile supplies the ports for this rule."
              >
                <ProfilePicker
                  profiles={profiles.data ?? []}
                  value={profile}
                  onChange={setProfile}
                />
              </Field>
            </TabsContent>
          </Tabs>
        </FormSection>

        <FormSection title="Source">
          <ChoiceGrid columns={2}>
            {SOURCE_PRESETS.map((preset) => (
              <ChoiceCard
                key={preset.key}
                selected={sourceKind === preset.key}
                onClick={() => {
                  setSourceKind(preset.key)
                  setFrom(preset.value)
                }}
                verb={`Use ${preset.label}`}
                title={preset.label}
                description={preset.hint}
                logo={
                  <ProductLogo
                    id={preset.key === "tailnet" ? "tailscale" : undefined}
                    fallback={
                      preset.key === "anywhere"
                        ? Globe
                        : preset.key === "private"
                          ? Router
                          : NetworkDevice
                    }
                    size="sm"
                  />
                }
              />
            ))}
          </ChoiceGrid>
          {sourceKind !== "anywhere" && (
            <Field label="Address or CIDR" htmlFor="rule-source">
              <Input
                id="rule-source"
                value={sourceKind === "custom" ? from : source}
                onChange={(event) => {
                  setSourceKind("custom")
                  setFrom(event.target.value)
                }}
                placeholder="10.0.0.0/8 or 203.0.113.9"
                className="font-mono"
              />
            </Field>
          )}
          {dangerous && (
            <Notice tone="danger" icon={Warning} title={`${matched?.name} open to the internet`}>
              {matched?.danger} Restrict the source or bind the service to loopback.
            </Notice>
          )}
        </FormSection>

        <Disclosure summary="Rule details" quiet>
          <div className="space-y-4">
            {!edit && (
              <Field
                label="Insert at"
                htmlFor="rule-position"
                hint="First match wins. Leave empty to append; address-only deny rules are inserted first by the server."
              >
                <Input
                  id="rule-position"
                  value={position}
                  inputMode="numeric"
                  onChange={(event) => setPosition(event.target.value)}
                  placeholder="End of the rule list"
                />
              </Field>
            )}
            <Field label="Comment" htmlFor="rule-comment">
              <Input
                id="rule-comment"
                value={comment}
                onChange={(event) => setComment(event.target.value)}
                placeholder="What this rule is for"
              />
            </Field>
          </div>
        </Disclosure>
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
          aria-label="Application profile"
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
