"use client"

import { useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** What `parse` makes of a typed entry: the value to add, or why it is refused. */
export type ListEntry = { entry: string } | { error: string }

/**
 * The entry as typed, trimmed. Nothing typed adds nothing; an entry already
 * listed is just done, because the servers behind these lists refuse one
 * listed twice.
 */
function plainEntry(typed: string, values: readonly string[]): ListEntry | null {
  const entry = typed.trim()
  if (!entry || values.includes(entry)) return null
  return { entry }
}

/**
 * Single values, added one at a time: what is listed as tags, each with its
 * own Remove, and under them a field and an Add. Addresses a site allows,
 * the tailnet logins a deployment admits, the headers a path sets.
 *
 * The proxy's site form and the deployment's Domains page each drew this —
 * the same add-one, remove-one control — with a different list under it, one
 * a bare run of tags and one a named list. It is the named one now, so a
 * reader hears what the tags are a list of.
 *
 * `parse` reads an entry as typed and may refuse it with a sentence, which
 * stands as the field's error until the entry is typed again; without it an
 * entry is taken as typed, trimmed. Enter adds, as the button does, and never
 * submits the form around it.
 */
export function ListField({
  id,
  label,
  placeholder,
  values,
  onChange,
  hint,
  info,
  error,
  disabled,
  parse,
  mono = true,
}: {
  id: string
  label: string
  placeholder: string
  values: string[]
  onChange: (values: string[]) => void
  hint?: React.ReactNode
  info?: React.ReactNode
  error?: React.ReactNode
  /** Read-only: the values without the controls that change them. */
  disabled?: boolean
  /** Reads an entry as typed; null adds nothing and clears the field. */
  parse?: (typed: string, values: readonly string[]) => ListEntry | null
  /** The values are literals — addresses, logins, header names — and are set in mono. */
  mono?: boolean
}) {
  const [typed, setTyped] = useState("")
  const [problem, setProblem] = useState<string>()
  const add = () => {
    const read = (parse ?? plainEntry)(typed, values)
    if (read && "error" in read) {
      setProblem(read.error)
      return
    }
    if (read) onChange([...values, read.entry])
    setTyped("")
    setProblem(undefined)
  }
  return (
    <Field label={label} htmlFor={id} hint={hint} info={info} error={problem ?? error}>
      <div data-slot="list-field" className="space-y-2">
        {values.length > 0 && (
          <ul aria-label={label} className="flex flex-wrap items-center gap-1.5">
            {values.map((value, index) => (
              <li key={`${value}-${index}`}>
                <Tag mono={mono} className="gap-1.5 pr-0.5">
                  {value}
                  {!disabled && (
                    <IconAction
                      type="button"
                      label={`Remove ${value}`}
                      size="icon-xs"
                      className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                      onClick={() => onChange(values.filter((_, at) => at !== index))}
                    >
                      <Trash />
                    </IconAction>
                  )}
                </Tag>
              </li>
            ))}
          </ul>
        )}
        {!disabled && (
          <div className="flex items-center gap-2">
            <Input
              id={id}
              value={typed}
              onChange={(event) => {
                setTyped(event.target.value)
                setProblem(undefined)
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  add()
                }
              }}
              placeholder={placeholder}
              autoComplete="off"
              spellCheck={false}
              className={cn(mono && "font-mono text-xs")}
            />
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={add}
              disabled={!typed.trim()}
              aria-label={`Add to ${label.toLowerCase()}`}
            >
              <Plus className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
    </Field>
  )
}
