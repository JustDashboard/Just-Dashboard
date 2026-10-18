"use client"

import { Disclosure, Field, FieldRow } from "@/components/form"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  defaultPort,
  type RdComputer,
  type RdComputerInput,
  type RdOS,
  type RdProtocol,
} from "@/lib/remote-desktop"

/** What the form holds: every field as the reader typed it. */
export type ComputerDraft = {
  name: string
  os: RdOS
  host: string
  protocol: RdProtocol
  port: string
  username: string
  domain: string
  password: string
  security: string
  layout: string
  peerId: string
  gfx: boolean
}

export function draftOf(computer: RdComputer): ComputerDraft {
  return {
    name: computer.name,
    os: computer.os,
    host: computer.host,
    protocol: computer.protocol,
    port: String(computer.port),
    username: computer.username,
    domain: computer.domain,
    password: "",
    security: computer.security,
    layout: computer.layout,
    peerId: computer.peerId,
    gfx: computer.gfx,
  }
}

/**
 * The draft as the API takes it. An empty password on an edit keeps the one
 * saved, which the backend reads as the field being left out.
 */
export function inputOf(draft: ComputerDraft, editing: boolean): RdComputerInput {
  const port = Number.parseInt(draft.port, 10)
  return {
    name: draft.name.trim(),
    os: draft.os,
    host: draft.host.trim(),
    protocol: draft.protocol,
    port: Number.isFinite(port) ? port : defaultPort(draft.protocol),
    username: draft.username.trim(),
    domain: draft.domain.trim(),
    password: editing && draft.password === "" ? undefined : draft.password,
    security: draft.protocol === "rdp" ? draft.security : "",
    layout: draft.protocol === "rdp" ? draft.layout : "",
    peerId: draft.peerId,
    gfx: draft.protocol === "rdp" && draft.gfx,
  }
}

const SECURITY = [
  { value: "any", label: "Let the computer choose" },
  { value: "nla", label: "Network Level Authentication" },
  { value: "tls", label: "TLS" },
  { value: "rdp", label: "Standard RDP" },
]

/**
 * How the dashboard signs in: the account and password, and the keyboard
 * the remote computer expects. The password is sent once and sealed on the
 * server; it is never shown again, only replaced.
 */
export function ComputerFields({
  draft,
  onChange,
  layouts,
  editing,
  id = "computer",
}: {
  draft: ComputerDraft
  onChange: (next: Partial<ComputerDraft>) => void
  layouts: string[]
  editing: boolean
  id?: string
}) {
  const rdp = draft.protocol === "rdp"
  const mac = draft.os === "macos"
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Field label="Name" htmlFor={`${id}-name`} hint="What the dashboard calls it.">
        <Input
          id={`${id}-name`}
          value={draft.name}
          onChange={(event) => onChange({ name: event.target.value })}
          autoComplete="off"
          maxLength={64}
        />
      </Field>
      <FieldRow>
        <Field
          label={mac ? "Mac account name" : "Username"}
          htmlFor={`${id}-username`}
          hint={
            mac
              ? "The short name, as whoami prints it in Terminal."
              : draft.os === "windows"
                ? "A Microsoft account signs in with its email address."
                : "The account on the computer."
          }
        >
          <Input
            id={`${id}-username`}
            value={draft.username}
            onChange={(event) => onChange({ username: event.target.value })}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        <Field
          label={mac ? "Mac login password" : "Password"}
          htmlFor={`${id}-password`}
          hint={
            editing
              ? "Leave empty to keep the saved one."
              : mac
                ? "The password you log in to this Mac with."
                : draft.os === "windows"
                  ? "The account's password, not its PIN."
                  : "Sealed on this server and never shown again."
          }
        >
          <Input
            id={`${id}-password`}
            type="password"
            value={draft.password}
            onChange={(event) => onChange({ password: event.target.value })}
            autoComplete="new-password"
            placeholder={editing ? "••••••••" : undefined}
          />
        </Field>
      </FieldRow>
      {rdp && (
        <Field
          label="Keyboard"
          htmlFor={`${id}-layout`}
          hint="The layout the computer types with, so every key lands where it should."
        >
          <Select
            value={draft.layout || "en-us-qwerty"}
            onValueChange={(layout) => onChange({ layout })}
          >
            <SelectTrigger id={`${id}-layout`} size="sm" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {layouts.map((layout) => (
                <SelectItem key={layout} value={layout}>
                  {layout}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
      <Disclosure quiet summary="Address and security">
        <div className="flex min-w-0 flex-col gap-4 pt-3">
          <FieldRow>
            <Field label="Address" htmlFor={`${id}-host`} hint="A tailnet name, or an IP address.">
              <Input
                id={`${id}-host`}
                className="font-mono"
                value={draft.host}
                onChange={(event) => onChange({ host: event.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Port"
              htmlFor={`${id}-port`}
              hint={`${defaultPort(draft.protocol)} unless it was moved.`}
            >
              <Input
                id={`${id}-port`}
                className="font-mono"
                inputMode="numeric"
                value={draft.port}
                onChange={(event) => onChange({ port: event.target.value.replace(/\D/g, "") })}
              />
            </Field>
          </FieldRow>
          {rdp && (
            <FieldRow>
              <Field label="Security" htmlFor={`${id}-security`}>
                <Select
                  value={draft.security || "any"}
                  onValueChange={(security) => onChange({ security })}
                >
                  <SelectTrigger id={`${id}-security`} size="sm" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SECURITY.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Domain"
                htmlFor={`${id}-domain`}
                hint="Only for a PC in a Windows domain."
              >
                <Input
                  id={`${id}-domain`}
                  value={draft.domain}
                  onChange={(event) => onChange({ domain: event.target.value })}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
            </FieldRow>
          )}
          {rdp && (
            <label className="flex items-start gap-2.5 text-body">
              <Checkbox
                checked={draft.gfx}
                onCheckedChange={(on) => onChange({ gfx: on === true })}
                className="mt-0.5"
              />
              <span className="flex min-w-0 flex-col gap-0.5">
                <span>Use the RDP graphics pipeline</span>
                <span className="text-hint text-muted-foreground">
                  Off, the screen is drawn on the computer&apos;s own frame marks, which is smoother
                  and lighter. On only for a server that refuses to connect without it.
                </span>
              </span>
            </label>
          )}
        </div>
      </Disclosure>
    </div>
  )
}
