import { Database, Globe } from "@/components/icons"
import { ProductGlyph, ProductGlyphs } from "@/components/product-logo"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"

/**
 * What Add a database draws beside the catalogue until an engine is picked:
 * the thing about to be made, as a picture. The engine is a mark not added
 * yet, with the ones it could be drawn beside its name; a line runs from it
 * to where the server will be — a container on this machine, or the address
 * of one that already runs — and on to this dashboard, which opens it once
 * it answers.
 *
 * It was a dashed box holding three logos and a sentence: a frame around
 * nothing, on a screen whose other half is a list of things with their own
 * marks. The words are the box's own, said on the node each belongs to.
 *
 * Only at the width that draws the two columns: stacked, a promise above the
 * catalogue is a block of nothing between the reader and it.
 */
export function EnginePicture({
  way,
  engines,
}: {
  /** Whether the server is to be started here or is one that already runs. */
  way: "start" | "connect"
  /** The products the catalogue leads with, as `product-logo` ids. */
  engines: string[]
}) {
  const marks = engines.slice(0, 3)
  return (
    <div
      role="img"
      aria-label={
        way === "start"
          ? "An engine, started in a container on this server and connected to this dashboard"
          : "An engine, reached at its address and saved in this dashboard"
      }
      data-slot="engine-picture"
      className="hidden min-w-0 px-2 pt-1 xl:block xl:self-start"
    >
      <div className="relative flex flex-col gap-8">
        {/* Dashed and still: nothing is connected to anything yet. */}
        <span
          aria-hidden
          className="absolute top-8 bottom-8 left-[1.375rem] border-l border-dashed border-border-strong"
        />
        <WireNode
          // One ring, the width of the marks under it, so the three names
          // start on one edge; the engines it could be are drawn after it.
          mark={<WirePlaceholder size="md" fallback={Database} />}
          eyebrow="Engine"
          title={
            <span className="flex items-center gap-2">
              Pick an engine
              <ProductGlyphs ids={marks} max={3} />
            </span>
          }
          hint={way === "start" ? "Its settings open here." : "Its address opens here."}
        />
        <WireNode
          mark={
            <WireMark tone="logo" size="md">
              {way === "start" ? <ProductGlyph id="docker" /> : <Globe aria-hidden />}
            </WireMark>
          }
          eyebrow={way === "start" ? "Container" : "Where it runs"}
          title={way === "start" ? "Started on this server" : "Dialled at its address"}
          hint={
            way === "start"
              ? "Nothing is started until you create it."
              : "On this server or another machine."
          }
        />
        <WireNode
          mark={<WireHost size="md" />}
          eyebrow="This dashboard"
          title={way === "start" ? "Connected once it answers" : "Saved once it has answered"}
          hint={
            way === "start"
              ? "It opens on the database's home."
              : "Nothing is saved until it connects."
          }
        />
      </div>
    </div>
  )
}
