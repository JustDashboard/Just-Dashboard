package media

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Prowlarr is the Servarr application the person adds their sources to. Its
// API is /api/v1; what it shares its sources with are its "applications".

func prowlarrAuth(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrAuth(ctx, env, arrProwlarr)
}

// prowlarrApps makes Radarr and Sonarr two of Prowlarr's applications, at
// their Compose names, each with its own API key and full sync: a source
// added, changed or removed in Prowlarr is added, changed or removed in
// both. Prowlarr's own test of each (it calls the application with the key)
// runs before it is saved.
func prowlarrApps(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return stacks.Result{}, err
	}
	outputs := map[string]string{}
	var saved, kept []string
	for _, target := range []arrApp{arrRadarr, arrSonarr} {
		key, err := env.Variable(ctx, target.keyVariable)
		if err != nil {
			return stacks.Result{}, err
		}
		changed, id, err := client.ensureProvider(ctx, arrProvider{
			resource:       "applications",
			what:           "application",
			name:           target.title + arrOwnedSuffix,
			implementation: target.title,
			top:            map[string]any{"syncLevel": "fullSync"},
			fields: map[string]any{
				"prowlarrUrl": "http://prowlarr:" + strconv.Itoa(PortProwlarr),
				"baseUrl":     "http://" + target.service + ":" + strconv.Itoa(target.port),
			},
			secrets: map[string]string{"apiKey": key},
		})
		if err != nil {
			return stacks.Result{}, err
		}
		outputs[target.service+"ApplicationId"] = strconv.Itoa(id)
		if changed {
			saved = append(saved, target.title)
			env.Logf("Prowlarr's application %q was saved after its own test passed", target.title+arrOwnedSuffix)
		} else {
			kept = append(kept, target.title)
		}
	}
	message := "Prowlarr shares its sources with Radarr and Sonarr, kept fully in sync"
	if len(saved) == 0 {
		message = "Prowlarr already shares its sources with " + strings.Join(kept, " and ") + ", kept fully in sync"
	}
	return stacks.Result{Message: message, Outputs: outputs}, nil
}

// prowlarrIndexer is the part of GET indexer the stack reads.
type prowlarrIndexer struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Enable bool   `json:"enable"`
}

// prowlarrSources counts the sources the person has added and switched on.
// It never adds one: which sources a person may use is theirs to decide,
// and none ships with the stack.
func prowlarrSources(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return stacks.Result{}, err
	}
	var indexers []prowlarrIndexer
	if err := client.call(ctx, http.MethodGet, "indexer", nil, &indexers); err != nil {
		return stacks.Result{}, err
	}
	enabled := 0
	for _, indexer := range indexers {
		if indexer.Enable {
			enabled++
		}
	}
	if enabled == 0 {
		description := "Nothing can be searched for or downloaded until Prowlarr has a source. " +
			"None is included with the stack. Open Prowlarr, sign in, and add a source you are allowed to use under Indexers; " +
			"Prowlarr shares it with Radarr and Sonarr by itself."
		if len(indexers) > 0 {
			description = fmt.Sprintf("Prowlarr has %s, but none is switched on, so nothing can be searched for or downloaded. ", prowlarrCount(len(indexers))) +
				"Open Prowlarr, sign in, and enable a source you are allowed to use under Indexers, or add one; " +
				"Prowlarr shares it with Radarr and Sonarr by itself. None is included with the stack."
		}
		return stacks.Result{}, stacks.NeedsAction(stacks.Action{
			ID:          actionMediaSource,
			Service:     "prowlarr",
			Title:       "Add a media source",
			Description: description,
			Blocking:    true,
		})
	}
	return stacks.Result{
		Message: fmt.Sprintf("Prowlarr has %s switched on, shared with Radarr and Sonarr", prowlarrCount(enabled)),
		Outputs: map[string]string{"sources": strconv.Itoa(enabled)},
	}, nil
}

func prowlarrCount(count int) string {
	if count == 1 {
		return "1 media source"
	}
	return strconv.Itoa(count) + " media sources"
}
