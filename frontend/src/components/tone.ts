/**
 * The one severity vocabulary, and the one word for "this is the bad one".
 *
 * Four components each declared their own version of this, and they disagreed
 * on the name of the severe level: `status-dot` called it `critical`,
 * `stat-tile` and `tag` called it `danger`, and `Button` calls its variant
 * `destructive`. Three words for one idea meant every component boundary needed
 * a translation, and `Tone` imported from two different files was two
 * incompatible unions with the same name.
 *
 * `danger` wins because it describes the *reading* rather than the cause:
 * `critical` is what a health check reports, `destructive` is what a button
 * does, and `danger` is what the colour says. The first two are still the right
 * words in their own places — `Verdict` keeps `critical` because that is the
 * value the backend sends, and `Button`'s variant keeps `destructive` because
 * that is what the control is — but anything choosing a *colour* chooses from
 * here.
 */
export type Tone = "default" | "success" | "warning" | "danger"

/** The tones that carry a hue, for the callers that switch on only those. */
export type SignalTone = Exclude<Tone, "default">
