"use client"

import { Eye } from "@/components/icons"
import { scopeAllows, scopeName, SCOPES } from "@/lib/account-link"
import type { AccountLinkScope } from "@/lib/types"
import { LargeMarkTile, type Mark } from "@/components/account-link/marks"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"

/**
 * Each scope drawn as what it lets the site touch: Observe only reads, Operate reaches Docker,
 * Maintain reaches this dashboard itself.
 */
const MARK: Record<AccountLinkScope, Mark> = {
  observe: { glyph: Eye, hue: "var(--tag-blue)" },
  operate: { product: "docker" },
  maintain: { brand: true },
}

/**
 * The three scopes as the choice they are: a mark, a name and what the site may then ask this
 * server to do, in the contract's words. What each one shares is the list under the cards, which
 * follows the card picked — said once there rather than in three paragraphs here.
 */
export function ScopeCards({
  value,
  onChange,
  disabled,
}: {
  value: AccountLinkScope
  onChange: (scope: AccountLinkScope) => void
  disabled?: boolean
}) {
  return (
    <ChoiceGrid columns={3} role="group" aria-label="Scope">
      {SCOPES.map((scope, index) => (
        <ChoiceCard
          key={scope}
          verb={`Choose the ${scopeName(scope)} scope`}
          title={scopeName(scope)}
          description={scopeAllows(scope)}
          logo={<LargeMarkTile mark={MARK[scope]} />}
          index={index}
          selected={value === scope}
          disabled={disabled}
          onClick={() => onChange(scope)}
        />
      ))}
    </ChoiceGrid>
  )
}
