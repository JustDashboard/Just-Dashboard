"use client"

import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ProtectionView } from "@/lib/types"
import { Trash, Warning } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { DimActions, IconAction } from "@/components/icon-action"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Cidr } from "@/components/network/address"
import { ORIGIN_WORD } from "@/components/network/protection/reading"

/**
 * The addresses no blocklist and no limit may ever refuse: this machine, the
 * dashboard's own allowlist, the address this browser is reading from, and
 * any the reader has kept. Every drop the gateway makes is preceded by this
 * set, which is why a country list cannot lock the operator out. Each says
 * where it came from, and a removable one can be taken out — the server
 * refuses to forget the reader's own address while nothing else would still
 * cover it.
 *
 * When this browser's address is not in the set a notice says so, because it
 * is then the one address a list or a limit may refuse.
 */
export function TrustedList({
  view,
  onChanged,
}: {
  view: Pick<ProtectionView, "trusted" | "client" | "clientTrusted">
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const remove = (address: string, you: boolean) =>
    confirm({
      title: `Stop trusting ${address}`,
      confirmLabel: "Stop trusting",
      description: (
        <p>
          {you
            ? "This is the address you are reading this page from. Blocklists and limits may refuse it from now on."
            : "Blocklists and limits may refuse it from now on."}
        </p>
      ),
      action: async () => {
        await del("/network/protection/trusted", { query: { address } })
        notify.success(`${address} is no longer trusted`)
        onChanged()
      },
    })
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {!view.clientTrusted && (
        <Notice tone="warning" icon={Warning} title="Your own address is not trusted">
          This browser reads the dashboard from{" "}
          <span className="font-mono text-foreground">{view.client}</span>, which no entry below
          covers, so a blocklist or a limit that matches it would cut you off.
        </Notice>
      )}
      <RowList>
        {view.trusted.map((entry) => (
          <Row
            key={entry.address}
            title={<Cidr cidr={entry.address} />}
            trailing={
              <>
                <Tag>{ORIGIN_WORD[entry.origin]}</Tag>
                {entry.removable ? (
                  <DimActions>
                    <IconAction
                      label={`Stop trusting ${entry.address}`}
                      onClick={() => remove(entry.address, entry.origin === "you")}
                    >
                      <Trash aria-hidden />
                    </IconAction>
                  </DimActions>
                ) : (
                  // The width of the action it does not have, so the origin
                  // words of a column of rows line up.
                  <span aria-hidden className="size-8 shrink-0" />
                )}
              </>
            }
          />
        ))}
      </RowList>
      {dialog}
    </div>
  )
}
