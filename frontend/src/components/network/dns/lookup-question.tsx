"use client"

import { cn } from "@/lib/utils"
import { Field } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export const RECORD_TYPES = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR", "SRV"]

/**
 * The question every lookup on the page begins with: a name, the record asked
 * for where the lookup has one, and the lookup's verb at the end of the name.
 * The three lookups each drew this form under a paragraph of their own; they
 * share the name now, so a name resolved is the name investigated next, and
 * what each lookup does is behind the ⓘ beside the label.
 */
export function Question({
  id,
  name,
  onName,
  type,
  onType,
  verb,
  ready,
  busy,
  error,
  info,
  children,
  onSubmit,
}: {
  /** The name field's id: `lookup-name` is the one the page's command puts the caret in. */
  id: string
  name: string
  onName: (name: string) => void
  type?: string
  /** Given, the question has a record type. */
  onType?: (type: string) => void
  verb: string
  ready: boolean
  busy: boolean
  error?: React.ReactNode
  info: React.ReactNode
  /** A field of the lookup's own, after the record. */
  children?: React.ReactNode
  onSubmit: () => void
}) {
  return (
    <form
      className={cn(
        "grid min-w-0 gap-3 sm:items-start",
        onType && children
          ? "sm:grid-cols-[minmax(0,1fr)_7rem] lg:grid-cols-[minmax(0,1fr)_7rem_minmax(0,14rem)]"
          : onType && "sm:grid-cols-[minmax(0,1fr)_7rem]",
      )}
      onSubmit={(event) => {
        event.preventDefault()
        if (ready && !busy) onSubmit()
      }}
    >
      <Field label="Name" htmlFor={id} error={error} info={info}>
        <InputGroup>
          <InputGroupInput
            id={id}
            value={name}
            onChange={(event) => onName(event.target.value)}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            placeholder={type === "PTR" ? "192.0.2.10" : "example.com"}
          />
          <InputGroupAddon align="inline-end" className="gap-0 p-0">
            <InputGroupButton type="submit" disabled={!ready} pending={busy}>
              {verb}
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
      </Field>
      {onType && (
        <Field label="Record" htmlFor={`${id}-type`}>
          <Select value={type} onValueChange={onType}>
            <SelectTrigger id={`${id}-type`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RECORD_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
      {children}
    </form>
  )
}
