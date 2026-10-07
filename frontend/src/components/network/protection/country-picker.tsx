"use client"

import { useState } from "react"
import { COUNTRIES, countryName, flag, searchCountries } from "@/lib/countries"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"

/** The most countries one list covers; the server refuses more. */
export const MAX_COUNTRIES = 30

/**
 * Which countries a list blocks, as a searchable list of checkboxes with their
 * flags. Typing narrows the list by name or by code; a country stays ticked
 * when the search hides it, and the chosen ones are named above the list so
 * the reader never has to scroll to find out what is in it. Values are
 * lower-case codes, which is how the server takes them.
 *
 * A list rather than a popover because it sits inside a dialog already, and a
 * popover inside a dialog is two layers fighting over the keyboard.
 */
export function CountryPicker({
  value,
  onChange,
}: {
  value: string[]
  onChange: (codes: string[]) => void
}) {
  const [query, setQuery] = useState("")
  const shown = searchCountries(query)
  const chosen = new Set(value)
  const toggle = (code: string, on: boolean) => {
    const lower = code.toLowerCase()
    onChange(on ? [...value.filter((c) => c !== lower), lower] : value.filter((c) => c !== lower))
  }
  const full = value.length >= MAX_COUNTRIES
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-center justify-between gap-3">
        <p className="min-w-0 truncate text-hint text-muted-foreground" aria-live="polite">
          {value.length === 0 ? (
            "None chosen"
          ) : (
            <>
              <span className="numeric text-foreground">{value.length}</span> of {MAX_COUNTRIES}:{" "}
              {value.map((c) => `${flag(c)} ${countryName(c)}`).join(" · ")}
            </>
          )}
        </p>
        {value.length > 0 && (
          <Button type="button" size="xs" variant="ghost" onClick={() => onChange([])}>
            Clear
          </Button>
        )}
      </div>
      <Input
        type="search"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        placeholder={`Search ${COUNTRIES.length} countries by name or code`}
        aria-label="Search countries"
        autoComplete="off"
      />
      <Group className="p-0">
        <ul className="max-h-64 divide-y divide-hairline overflow-y-auto">
          {shown.map(({ code, name }) => {
            const on = chosen.has(code.toLowerCase())
            return (
              <li key={code}>
                <label className="flex cursor-pointer items-center gap-3 px-3 py-2 transition-colors hover:bg-row-hover">
                  <Checkbox
                    checked={on}
                    disabled={!on && full}
                    onCheckedChange={(next) => toggle(code, next === true)}
                  />
                  <span aria-hidden className="w-6 text-base leading-none">
                    {flag(code)}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-body">{name}</span>
                  <span className="font-mono text-micro text-muted-foreground">{code}</span>
                </label>
              </li>
            )
          })}
          {shown.length === 0 && (
            <li className="px-3 py-6 text-center text-body text-muted-foreground">
              No country matches &ldquo;{query}&rdquo;.
            </li>
          )}
        </ul>
      </Group>
    </div>
  )
}
