"use client"

import { useState } from "react"
import { Copy, Eye, Sparkles } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { InitialsMark } from "@/components/account/user-avatar"
import { Field, FormNote } from "@/components/form"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"
import { generatePassword } from "@/components/database/connect/rules"
import { nameHue } from "@/components/database/home/kinds"
import { connectFormats, connectSnippet } from "@/components/database/shell/connection-string"
import { useDatabase } from "@/components/database/shell/database-context"

/** An account drawn from its name: square initials in the name's own hue. */
export function AccountMark({ name, size = "md" }: { name: string; size?: "sm" | "md" }) {
  return <InitialsMark name={name} size={size} />
}

/** An account's name in its hue — the same one its mark has — with its host quiet after it. */
export function AccountName({
  name,
  host,
  className,
}: {
  name: string
  host?: string
  className?: string
}) {
  return (
    <span className={cn("font-mono text-xs", className)}>
      <span style={{ color: nameHue(name) }}>{name}</span>
      {host && <span className="text-muted-foreground">@{host}</span>}
    </span>
  )
}

/**
 * A password the dashboard makes or the reader types: shown or hidden,
 * generated again, copied. It is one box with its controls inside it.
 */
export function SecretField({
  id,
  label,
  hint,
  value,
  onChange,
  error,
}: {
  id: string
  label: string
  hint?: React.ReactNode
  value: string
  onChange: (value: string) => void
  error?: string
}) {
  const [shown, setShown] = useState(true)
  return (
    <Field label={label} htmlFor={id} hint={hint} error={error}>
      <InputGroup>
        <InputGroupInput
          id={id}
          type={shown ? "text" : "password"}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          autoComplete="new-password"
          spellCheck={false}
          className="font-mono"
        />
        <InputGroupAddon align="inline-end" className="gap-0 p-0">
          <InputGroupToggle
            icon={Eye}
            label="Show"
            aria-label="Show the password"
            pressed={shown}
            onPressedChange={setShown}
          />
          <InputGroupButton
            aria-label="Generate another password"
            onClick={() => onChange(generatePassword())}
          >
            <Sparkles className="size-3.5" />
            <span className="max-sm:hidden">Generate</span>
          </InputGroupButton>
          <InputGroupButton
            aria-label="Copy the password"
            disabled={!value}
            onClick={() => void copyText(value, "Password copied")}
          >
            <Copy className="size-3.5" />
            <span className="max-sm:hidden">Copy</span>
          </InputGroupButton>
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )
}

/**
 * How a program connects as one account: the string in the shapes the
 * Connect panel offers — a URL, a line of an `.env` file, the engine's own
 * shell, a client library's few lines — with the account's password in it.
 */
export function AccountSnippet({
  dsn,
  user,
  database,
}: {
  /** The connection string as the driver takes it. */
  dsn: string
  user: string
  database: string
}) {
  const { conn, engine } = useDatabase()
  const formats = connectFormats(engine)
  const [format, setFormat] = useState(formats[0]?.id ?? "url")
  const snippet = connectSnippet(
    engine,
    { url: engine.url(dsn), host: conn.host, port: conn.port, user, database },
    format,
  )
  return (
    <div className="space-y-2" data-slot="account-snippet">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1.5">
        <ChipStrip role="group" aria-label="The shape of the connection string">
          {formats.map((one) => (
            <FilterChip key={one.id} selected={format === one.id} onClick={() => setFormat(one.id)}>
              {one.label}
            </FilterChip>
          ))}
        </ChipStrip>
        <Button
          size="xs"
          variant="ghost"
          onClick={() => void copyText(snippet, "Connection string copied")}
        >
          <Copy />
          Copy
        </Button>
      </div>
      <Well className="max-h-48 overflow-auto text-hint leading-relaxed break-all whitespace-pre-wrap">
        {snippet}
      </Well>
    </div>
  )
}

/**
 * The last step of making an account or changing its password: the secret,
 * shown once, with the string a program connects with. Nothing keeps the
 * password after this — the server stores a hash — so the dialog stays until
 * the reader says it is saved.
 */
export function SecretShown({
  title,
  password,
  dsn,
  user,
  database,
  notes,
}: {
  title: string
  password: string
  dsn: string
  user: string
  database: string
  /** What else the server said about what it did. */
  notes?: string[]
}) {
  return (
    <div className="space-y-4" data-slot="secret-shown">
      <Notice tone="success" title={title}>
        The password is shown here once. The server keeps only a hash of it, and so does nobody
        else: copy it, or the string below, before closing.
      </Notice>
      <div className="flex min-w-0 items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded-md bg-surface-sunken px-3 py-2 font-mono text-xs">
          {password}
        </code>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void copyText(password, "Password copied")}
        >
          <Copy />
          Copy password
        </Button>
      </div>
      <div className="space-y-1.5">
        <p className="text-body font-medium">Connect as {user}</p>
        <AccountSnippet dsn={dsn} user={user} database={database} />
        <FormNote>
          The address is the one this dashboard dials, and the string carries none of the options
          this connection is saved with — a TLS mode, a replica set: add the ones the server asks
          for. From another machine, use the address Connect gives for it.
        </FormNote>
      </div>
      {notes?.map((note) => (
        <FormNote key={note}>{note}</FormNote>
      ))}
    </div>
  )
}
