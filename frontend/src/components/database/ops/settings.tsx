"use client"

import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { FormSections } from "@/components/form"
import { SectionFrame } from "@/components/database/kit"
import { ConnectionSection } from "@/components/database/ops/settings-connection"
import { DangerSection } from "@/components/database/ops/settings-danger"
import { ServerDatabasesSection } from "@/components/database/ops/settings-databases"
import { ExtensionsSection } from "@/components/database/ops/settings-extensions"
import {
  SectionJump,
  useSectionAnchor,
  type SettingsSection,
} from "@/components/database/ops/settings-nav"
import { ParametersSection } from "@/components/database/ops/settings-parameters"
import { ReachabilitySection } from "@/components/database/ops/settings-reach"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * One database's settings: the connection the dashboard keeps, where the
 * server is reachable from, the server's own parameters and extensions, what
 * else it holds, and the acts that end it.
 *
 * The page is a form, so it is one column of sections (§7), each head over
 * its fields carrying what the section currently is — the engine and its
 * address, the reach, how many parameters differ from their default. It keeps
 * no tiles: every figure it has is one of those heads' lines.
 *
 * A section the engine lacks is not drawn. A file has no server, so no
 * reachability and no neighbours; a key–value server has modules where a SQL
 * engine has extensions, and its parameters come from its own configuration.
 * The danger zone is an administrator's, and the page's one frame.
 *
 * Every section has an anchor (`#connection`, `#reachability`, `#parameters`,
 * `#extensions`, `#databases`, `#danger`), and `?q=` narrows the parameters,
 * so a link from another page lands on one of them. The same anchors are the
 * strip at the top of the page: it is long, and its last section is the one a
 * reader most often comes for.
 */
export function Settings() {
  const { engine, param } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  // Every dialog here is opened by state: this hands the keyboard back to
  // the control that opened one.
  useFocusReturn()
  useSectionAnchor(param("q") || param("only") ? "parameters" : "")
  const server = engine.can("server")
  const keyvalue = engine.kind === "keyvalue"
  // A document database answers the parameters route with the counters of
  // its status command: readings, which are Performance's to draw.
  const parameters = engine.can("settings") && engine.kind !== "document"
  const extensions = engine.can("extensions") || keyvalue
  const danger = can("system.admin") && can("destructive")
  const sections: SettingsSection[] = [
    { id: "connection", title: "Connection" },
    ...(server ? [{ id: "reachability", title: "Reachability" }] : []),
    ...(parameters
      ? [{ id: "parameters", title: engine.can("fileBased") ? "Parameters" : "Server parameters" }]
      : []),
    ...(extensions ? [{ id: "extensions", title: keyvalue ? "Modules" : "Extensions" }] : []),
    ...(server
      ? [
          {
            id: "databases",
            title: engine.can("logicalDatabases") ? "Numbered databases" : "Databases",
          },
        ]
      : []),
    ...(danger ? [{ id: "danger", title: "Danger zone" }] : []),
  ]
  return (
    <SectionFrame section="settings">
      {sections.length > 2 && <SectionJump sections={sections} />}
      <FormSections className="mx-auto">
        <ConnectionSection />
        {server && <ReachabilitySection confirm={confirm} />}
        {parameters && <ParametersSection />}
        {extensions && <ExtensionsSection confirm={confirm} />}
        {server && <ServerDatabasesSection />}
        {danger && <DangerSection confirm={confirm} />}
      </FormSections>
      {dialog}
    </SectionFrame>
  )
}
