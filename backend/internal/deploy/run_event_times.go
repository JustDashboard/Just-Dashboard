package deploy

import "time"

// withEventTimes adds the instants a run.state or step.state event moves its
// subject to: a step's startedAt and endedAt, a run's claimedAt and endedAt.
// The run page reads the snapshot once and follows the events after it, so a
// time that is only in the table reaches it only on a reload. Each pair is a
// key and its time; a nil time is left out, as the snapshot leaves it out.
func withEventTimes(payload map[string]any, pairs ...any) map[string]any {
	for index := 0; index+1 < len(pairs); index += 2 {
		key, _ := pairs[index].(string)
		at, _ := pairs[index+1].(*time.Time)
		if key == "" || at == nil || at.IsZero() {
			continue
		}
		payload[key] = at.UTC()
	}
	return payload
}
