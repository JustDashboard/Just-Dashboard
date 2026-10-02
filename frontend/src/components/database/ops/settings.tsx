"use client"

import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { FormSections } from "@/components/form"
import { SectionFrame } from "@/components/database/kit"
import { ConnectionSection } from "@/components/database/ops/settings-connection"
import { DangerSection } from "@/components/database/ops/settings-danger"
import { ServerDatabasesSection } from "@/components/database/ops/settings-databases"
import { ExtensionsSection } from "@/components/database/ops/settings-extensions"
import { ParametersSection } from "@/components/database/ops/settings-parameters"
import { ReachabilitySection } from "@/components/database/ops/settings-reach"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * One database's settings: the connection the dashboard keeps, where the
 * server is reachable from, the server's own parameters and extensions, what
 * else it holds, and the acts that end it.
 *
 * The page is a form, so its sections stand in a rail (§7): the heads down
 * the left, each carrying what the section currently is — the engine and its
 * address, the reach, how many parameters differ from their default — and the
 * fields down the right. It keeps no tiles: every figure it has is one of
 * those heads' lines.
 *
 * A section the engine lacks is not drawn. A file has no server, so no
 * reachability and no neighbours; a key–value server has modules where a SQL
 * engine has extensions, and its parameters come from its own configuration.
 * The danger zone is an administrator's, and the page's one frame.
 *
 * Every section has an anchor (`#connection`, `#reachability`, `#parameters`,
 * `#extensions`, `#databases`, `#danger`), and `?q=` narrows the parameters,
 * so a link from another page can land on one of them.
 */
export function Settings() {
  const { engine } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const server = engine.can("server")
  return (
    <SectionFrame section="settings">
      <FormSections railFrom="xl">
        <ConnectionSection />
        {server && <ReachabilitySection confirm={confirm} />}
        {engine.can("settings") && <ParametersSection />}
        {(engine.can("extensions") || engine.kind === "keyvalue") && (
          <ExtensionsSection confirm={confirm} />
        )}
        {server && <ServerDatabasesSection />}
        {can("system.admin") && can("destructive") && <DangerSection confirm={confirm} />}
      </FormSections>
      {dialog}
    </SectionFrame>
  )
}
