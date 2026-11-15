"use client"

import { useState } from "react"
import { Copy, Eye, Sparkles } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { generatePassword } from "@/lib/password"
import { Field } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"

/**
 * A password the dashboard makes or the reader types: shown or hidden, made
 * again, copied. One box with its controls inside it, so the buttons sit on
 * the line of the value they act on rather than on the label's.
 *
 * It lived in the databases' Access page and was borrowed from there by the
 * system users' forms, while the proxy's login dialog and the found-server
 * dialog each drew their own with a different generator. Every "Generate" in
 * the product now makes the same kind of password (`generatePassword`).
 *
 * Shown by default: a password the dashboard made is one the reader has to
 * read, copy or write down, and hiding it would make them press Show first.
 * A password the reader types takes `shown={false}`.
 */
export function SecretField({
  id,
  label,
  hint,
  value,
  onChange,
  error,
  shown: initiallyShown = true,
  generate = true,
  copy = true,
  length,
}: {
  id: string
  label: string
  hint?: React.ReactNode
  value: string
  onChange: (value: string) => void
  error?: React.ReactNode
  /** Whether the value starts in clear text. */
  shown?: boolean
  /** Offer Generate, which replaces the value with a new made password. */
  generate?: boolean
  /** Offer Copy. */
  copy?: boolean
  /** How long a generated password is; `generatePassword`'s own default otherwise. */
  length?: number
}) {
  const [shown, setShown] = useState(initiallyShown)
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
          {generate && (
            <InputGroupButton
              aria-label="Generate another password"
              onClick={() => onChange(generatePassword(length))}
            >
              <Sparkles className="size-3.5" />
              <span className="max-sm:hidden">Generate</span>
            </InputGroupButton>
          )}
          {copy && (
            <InputGroupButton
              aria-label="Copy the password"
              disabled={!value}
              onClick={() => void copyText(value, "Password copied")}
            >
              <Copy className="size-3.5" />
              <span className="max-sm:hidden">Copy</span>
            </InputGroupButton>
          )}
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )
}
