"use client"

import { useId } from "react"
import { Router } from "@/components/icons"
import type { DbDriver } from "@/lib/types"
import { Segments } from "@/components/deploy/settings/segments"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Field, FieldRow, FormNote } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { Engine } from "@/components/database/engine"
import {
  TLS_WORD,
  maskPasted,
  maskedDsn,
  tlsModes,
  type ConnectFields,
} from "@/components/database/connect/dsn"
import { sourceDsn, type SourceDraft } from "@/components/database/import/model"
import { EngineMark } from "@/components/database/kit"

/**
 * The first step: where the database is now. The address is written as the
 * fields an operator knows, or pasted whole for one with an option the fields
 * do not offer — the same two shapes the connect form takes, and the same
 * strings, because this is a connection string to somebody else's server and
 * not a new kind of thing.
 *
 * Nothing typed here is saved. It stays in this tab's memory until the import
 * starts, and the server seals it only for as long as a replication needs it.
 */
export function SourceStep({
  engine,
  driver,
  draft,
  checking,
  refusal,
  onDraft,
  onCheck,
}: {
  engine: Engine
  driver: DbDriver
  draft: SourceDraft
  checking: boolean
  /** Why the source could not be read, in the server's words. */
  refusal?: string
  onDraft: (draft: SourceDraft) => void
  onCheck: () => void
}) {
  const id = useId()
  const dsn = sourceDsn(driver, draft)
  const modes = tlsModes(driver)
  const set = (patch: Partial<ConnectFields>) =>
    onDraft({ ...draft, fields: { ...draft.fields, ...patch } })

  return (
    <FlowPanel aria-label="Source" aria-busy={checking} className="min-w-0">
      <FlowPanelHeader
        title={
          <span className="flex min-w-0 items-center gap-2.5">
            <EngineMark engine={engine} size="sm" />
            <span className="truncate">The server it is on now</span>
          </span>
        }
        actions={
          <Segments
            label="How the address is given"
            value={draft.shape}
            onChange={(shape) => onDraft({ ...draft, shape })}
            options={[
              { value: "fields", label: "Fields" },
              { value: "url", label: "URL" },
            ]}
          />
        }
      />
      <FlowPanelBody className="space-y-4">
        {refusal && (
          <Notice tone="danger" title="The source could not be read">
            <span className="break-words whitespace-pre-wrap">{refusal}</span>
          </Notice>
        )}
        {draft.shape === "url" ? (
          <Field
            label="Connection string"
            htmlFor={`${id}-url`}
            hint="Pasted whole, as the application that uses it would write it."
          >
            <Input
              id={`${id}-url`}
              // A connection string is a password with an address round it.
              type="password"
              value={draft.pasted}
              onChange={(event) => onDraft({ ...draft, pasted: event.target.value })}
              placeholder={engine.dsnExample}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        ) : (
          <>
            <FieldRow columns={3}>
              <Field label="Host" htmlFor={`${id}-host`} className="sm:col-span-2">
                <Input
                  id={`${id}-host`}
                  value={draft.fields.host}
                  onChange={(event) => set({ host: event.target.value })}
                  placeholder="db.example.com"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label="Port" htmlFor={`${id}-port`}>
                <Input
                  id={`${id}-port`}
                  value={draft.fields.port}
                  onChange={(event) => set({ port: event.target.value.replace(/[^0-9]/g, "") })}
                  placeholder={engine.defaultPort}
                  className="font-mono"
                  inputMode="numeric"
                  autoComplete="off"
                />
              </Field>
            </FieldRow>
            <FieldRow>
              <Field label="User" htmlFor={`${id}-user`}>
                <Input
                  id={`${id}-user`}
                  value={draft.fields.user}
                  onChange={(event) => set({ user: event.target.value })}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label="Password" htmlFor={`${id}-password`}>
                <Input
                  id={`${id}-password`}
                  type="password"
                  value={draft.fields.password}
                  onChange={(event) => set({ password: event.target.value })}
                  className="font-mono"
                  autoComplete="new-password"
                />
              </Field>
            </FieldRow>
            <FieldRow>
              <Field label={engine.databaseField} htmlFor={`${id}-database`}>
                <Input
                  id={`${id}-database`}
                  value={draft.fields.database}
                  onChange={(event) => set({ database: event.target.value })}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              {engine.kind === "document" && (
                <Field
                  label="Account's database"
                  htmlFor={`${id}-auth`}
                  hint="Where the account was created, when it is not this one."
                >
                  <Input
                    id={`${id}-auth`}
                    value={draft.fields.authSource}
                    onChange={(event) => set({ authSource: event.target.value })}
                    placeholder="admin"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
              )}
            </FieldRow>
            {modes.length > 1 && (
              <Field
                label="Encryption"
                hint="Required encrypts without checking who answers; verified also checks the server's certificate."
              >
                <Segments
                  label="Encryption"
                  value={modes.includes(draft.fields.tls) ? draft.fields.tls : modes[0]}
                  onChange={(tls) => set({ tls })}
                  options={modes.map((mode) => ({ value: mode, label: TLS_WORD[mode] }))}
                />
              </Field>
            )}
          </>
        )}
        {dsn && (
          <FormNote className="truncate font-mono" data-slot="connection-preview">
            {draft.shape === "url" ? maskPasted(dsn) : maskedDsn(driver, draft.fields)}
          </FormNote>
        )}
      </FlowPanelBody>
      <FlowActions note="The password is sealed on the server, kept only while a replication needs it, and never sent back to a browser.">
        <Button className="h-11 sm:h-9" disabled={!dsn} pending={checking} onClick={onCheck}>
          <Router />
          Check the source
        </Button>
      </FlowActions>
    </FlowPanel>
  )
}
