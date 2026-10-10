"use client"

import { Field } from "@/components/form"
import { FilterChip } from "@/components/tabs"
import { Input } from "@/components/ui/input"
import { ENVIRONMENT, ENVIRONMENTS } from "@/components/database/connect/rules"
import { environmentHue } from "@/components/database/kit"

/** Why an environment word would be refused, or nothing when the server takes it. */
export function environmentProblem(word: string): string | undefined {
  const typed = word.trim()
  if (!typed || ENVIRONMENT.test(typed)) return undefined
  return "Up to 32 letters, digits, spaces, dots, dashes and underscores, starting with a letter or digit."
}

/**
 * What a database is for, as the operator says it: a word of their own, or
 * one of the four everybody has.
 *
 * The label is free text on the server — "eu-west qa" is as good a word as
 * "staging" — so it is a field, and the four common words are a press each
 * under it, in the hue their tag is drawn in everywhere else. A segmented
 * control held it to five answers and, on a phone, ran off the panel doing
 * so.
 */
export function EnvironmentField({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (value: string) => void
}) {
  const chosen = value.trim().toLowerCase()
  return (
    <Field
      label="Environment"
      htmlFor={id}
      hint="What it is for, as its tag says it everywhere. Empty for none."
      error={environmentProblem(value)}
    >
      <Input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder="none"
        maxLength={32}
        autoComplete="off"
        spellCheck={false}
        className="font-mono"
      />
      <div role="group" aria-label="The usual four" className="flex flex-wrap gap-1">
        {ENVIRONMENTS.map((word) => (
          <FilterChip
            key={word}
            selected={chosen === word}
            // A second press takes the word away again.
            onClick={() => onChange(chosen === word ? "" : word)}
            className="font-mono"
          >
            <span style={{ color: environmentHue(word) }}>{word}</span>
          </FilterChip>
        ))}
      </div>
    </Field>
  )
}
