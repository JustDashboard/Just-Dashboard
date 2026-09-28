"use client"

import type { Role } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { ROLE_MARK, ROLE_SUMMARY } from "@/components/account/capabilities"

const ROLES: Role[] = ["admin", "limited", "readonly"]

/**
 * Handing out a role, as the things it can be rather than a select of three
 * words. "limited" means nothing to somebody making their first key, and a
 * select could only show the sentence for the one already picked — the
 * choice is between the sentences, so all of them are on the cards (§4: a
 * choice between kinds is a card, and a card you pick carries the lit edge).
 */
export function RoleChoice({
  label,
  value,
  onChange,
  roles = ROLES,
}: {
  label: string
  value: Role
  onChange: (role: Role) => void
  /** The roles this person may hand out; a key never outranks its owner. */
  roles?: Role[]
}) {
  return (
    <fieldset className="min-w-0 space-y-1.5">
      <legend className="mb-1.5 text-body font-medium">{label}</legend>
      <div className="grid gap-2">
        {roles.map((role) => {
          const Mark = ROLE_MARK[role]
          const selected = value === role
          return (
            <ChoiceCard
              key={role}
              selected={selected}
              onClick={() => onChange(role)}
              className="min-h-0 flex-row items-center gap-3 px-3 py-2.5"
            >
              <Mark
                aria-hidden
                className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
              />
              <span className="flex min-w-0 flex-col">
                <ChoiceCardTitle>{role}</ChoiceCardTitle>
                <ChoiceCardHint>{ROLE_SUMMARY[role]}</ChoiceCardHint>
              </span>
            </ChoiceCard>
          )
        })}
      </div>
    </fieldset>
  )
}
