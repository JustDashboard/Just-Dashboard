import { LANES, hueFor } from "@/lib/hue"
import { cn } from "@/lib/utils"
import { InitialsMark } from "@/components/account/user-avatar"

/**
 * A player drawn the way their game draws them.
 *
 * Minecraft shows a player as the front of their head, eight pixels by
 * eight. The real one is the player's skin, which lives on the game maker's
 * servers; this dashboard asks nobody outside the server for anything (the
 * page's image policy would refuse it anyway), so the head is drawn here from
 * the name alone: the eight-by-eight face, in the hue the name already has in
 * the console's replies, with the fringe and the set of the eyes picked by the
 * same name so two players on one hue are still two faces.
 *
 * A game with no such convention gets the product's own mark for a person
 * known only by name.
 */
export function PlayerFace({
  name,
  blueprintId,
  className,
}: {
  name: string
  /** Which game this is, as the blueprint names it. */
  blueprintId?: string
  className?: string
}) {
  if (!blueprintId?.startsWith("minecraft"))
    return <InitialsMark name={name} size="sm" className={className} />
  const hue = hueFor(name, LANES)
  const bits = hash(name)
  // Row 2 is where hair ends: each of its eight cells is hair or face.
  const fringe = Array.from({ length: 8 }, (_, x) => x === 0 || x === 7 || ((bits >> x) & 1) === 1)
  // Eyes one cell in from the sides, or two: narrow- and wide-set.
  const inset = (bits >> 8) & 1
  const shade = (amount: number) => `color-mix(in oklab, ${hue}, black ${amount}%)`
  return (
    <svg
      data-slot="player-face"
      viewBox="0 0 8 8"
      shapeRendering="crispEdges"
      aria-hidden
      className={cn("size-7 shrink-0 rounded-md", className)}
    >
      <rect width="8" height="8" fill={hue} />
      {/* Hair: two full rows, then the fringe. */}
      <rect width="8" height="2" fill={shade(45)} />
      {fringe.map(
        (hair, x) => hair && <rect key={x} x={x} y={2} width="1" height="1" fill={shade(45)} />,
      )}
      {/* Eyes: a white cell and a pupil beside it, mirrored. */}
      <rect x={1 + inset} y={4} width="1" height="1" fill="white" />
      <rect x={2 + inset} y={4} width="1" height="1" fill={shade(70)} />
      <rect x={5 - inset} y={4} width="1" height="1" fill={shade(70)} />
      <rect x={6 - inset} y={4} width="1" height="1" fill="white" />
      {/* Nose and mouth, a step darker than the face. */}
      <rect x={3} y={5} width="2" height="1" fill={shade(18)} />
      <rect x={2} y={6} width="4" height="1" fill={shade(32)} />
    </svg>
  )
}

function hash(name: string) {
  let value = 0
  for (let i = 0; i < name.length; i++) value = (value * 33 + name.charCodeAt(i)) >>> 0
  return value
}
