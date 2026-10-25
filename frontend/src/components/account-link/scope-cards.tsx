"use client"

import { scopeAllows, scopeName, scopeShares, SCOPES } from "@/lib/account-link"
import type { AccountLinkScope } from "@/lib/types"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"

/**
 * The three scopes as the choice they are. Each card says what the heartbeat carries and what the
 * site may then ask this server to do, in the contract's words, because "Operate" alone tells
 * nobody that it adds the names of their containers and projects to what leaves the machine.
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
          description={
            <span className="mt-1 flex flex-col gap-2.5">
              <span className="block">
                <span className="eyebrow block">Shares</span>
                {scopeShares(scope)}
              </span>
              <span className="block">
                <span className="eyebrow block">Lets the site</span>
                {scopeAllows(scope)}
              </span>
            </span>
          }
          index={index}
          selected={value === scope}
          disabled={disabled}
          onClick={() => onChange(scope)}
        />
      ))}
    </ChoiceGrid>
  )
}
