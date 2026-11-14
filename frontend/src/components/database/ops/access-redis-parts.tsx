"use client"

import { useId } from "react"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Field, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Textarea } from "@/components/ui/textarea"
import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import { aclSummary, ruleList, type AclDraft } from "@/components/database/ops/access-model"
import { AccountMark, AccountName, SecretField } from "@/components/database/ops/access-parts"
import type { RedisACLChange, RedisACLUser } from "@/components/database/ops/access-types"

/**
 * What a key–value server's Access list and a user's own page both draw: a
 * user's tags, the form of a rule's parts, and the question asked before a
 * user is deleted.
 */

/** The command categories an ACL rule is most often written with. */
const CATEGORIES = ["+@all", "+@read", "+@write", "+@keyspace", "-@dangerous", "-@admin"]

export function tagsOf(user: RedisACLUser) {
  return (
    <>
      {user.self && <Tag>this connection</Tag>}
      {user.system && <Tag>default</Tag>}
      {!user.enabled && <Tag>off</Tag>}
      {user.noPassword && <Tag tone="warning">any password</Tag>}
    </>
  )
}

/** The question asked before a user is deleted, with the user named. */
export function redisDropRequest(
  id: number,
  user: RedisACLUser,
  onDeleted: () => void,
): ConfirmRequest {
  return {
    title: "Delete user",
    confirmLabel: "Delete",
    subject: {
      mark: <AccountMark name={user.name} />,
      name: <AccountName name={user.name} className="text-body" />,
      facts: <FormFact label="Rule">{aclSummary(user)}</FormFact>,
    },
    description: (
      <p>
        Removes the user from the server and closes the connections it holds. Whatever signs in with
        it stops signing in.
      </p>
    ),
    action: async () => {
      const answer = await del<RedisACLChange>(
        `/databases/${id}/redis/acl/${encodeURIComponent(user.name)}`,
      )
      notify.success(`Deleted ${user.name}`, { description: answer.notice })
      onDeleted()
      return "reported"
    },
  }
}

/**
 * The parts of a rule as a form: whether the user may connect, its password,
 * and the three lists — key patterns, channel patterns, command rules in the
 * order they apply. `held` is why the dashboard's own user keeps the parts
 * that would cut it off.
 */
export function AclFields({
  draft,
  onChange,
  held,
  exists,
  disabled,
  tips,
}: {
  draft: AclDraft
  onChange: (patch: Partial<AclDraft>) => void
  /** Why switching it off, or narrowing its commands and keys, is not offered. */
  held?: string
  /** The user exists: an empty password keeps the ones it has. */
  exists: boolean
  disabled?: boolean
  /**
   * On a settings page, what goes in each list is behind the label's ⓘ; in a
   * dialog that makes a user, it stays under the field it is typed into.
   */
  tips?: boolean
}) {
  const field = useId()
  const said = (text: string) => (tips ? { info: text } : { hint: text })
  const commands = ruleList(draft.commands)
  return (
    <div className="space-y-4">
      {held && <FormNote data-slot="acl-held">{held}</FormNote>}
      <OptionList>
        <OptionRow
          title="Switched on: it may connect"
          checked={draft.enabled}
          disabled={disabled || Boolean(held)}
          onCheckedChange={(enabled) => onChange({ enabled })}
        />
        <OptionRow
          title="Accept any password"
          hint="Anything that reaches the port signs in as this user."
          tone={draft.noPassword ? "warning" : "default"}
          checked={draft.noPassword}
          disabled={disabled}
          onCheckedChange={(noPassword) => onChange({ noPassword, password: "" })}
        />
      </OptionList>
      {!draft.noPassword && !disabled && (
        <SecretField
          id={`${field}-password`}
          label={exists ? "New password" : "Password"}
          hint={
            exists
              ? "Empty keeps the passwords it has. A new one replaces them all."
              : "Made here. Type another if one is wanted."
          }
          value={draft.password}
          onChange={(password) => onChange({ password })}
        />
      )}
      <Field
        label="Keys"
        htmlFor={`${field}-keys`}
        {...said(
          "Patterns of the keys it may touch, one to a line: app:*, %R~cache:* for read only. * is every key.",
        )}
      >
        <Textarea
          id={`${field}-keys`}
          value={draft.keys}
          disabled={disabled || Boolean(held)}
          onChange={(event) => onChange({ keys: event.target.value })}
          rows={2}
          spellCheck={false}
          className="font-mono text-xs"
        />
      </Field>
      <Field
        label="Commands"
        htmlFor={`${field}-commands`}
        {...said(
          "Rules in the order they apply, one to a line, starting from nothing: +@read, -@dangerous, +get, -config|set.",
        )}
      >
        <Textarea
          id={`${field}-commands`}
          value={draft.commands}
          disabled={disabled || Boolean(held)}
          onChange={(event) => onChange({ commands: event.target.value })}
          rows={3}
          spellCheck={false}
          className="font-mono text-xs"
        />
        {!disabled && !held && (
          <ChipStrip role="group" aria-label="Add a command category">
            {CATEGORIES.filter((one) => !commands.includes(one)).map((one) => (
              <FilterChip
                key={one}
                className="font-mono"
                onClick={() => onChange({ commands: [...commands, one].join("\n") })}
              >
                {one}
              </FilterChip>
            ))}
          </ChipStrip>
        )}
      </Field>
      <Field
        label="Channels"
        htmlFor={`${field}-channels`}
        {...said("Patterns of the Pub/Sub channels it may use, one to a line. Empty for none.")}
      >
        <Textarea
          id={`${field}-channels`}
          value={draft.channels}
          disabled={disabled}
          onChange={(event) => onChange({ channels: event.target.value })}
          rows={2}
          spellCheck={false}
          className="font-mono text-xs"
        />
      </Field>
    </div>
  )
}
