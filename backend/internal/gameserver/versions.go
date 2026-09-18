// Package gameserver holds the game-specific adapters a game blueprint needs:
// which upstream versions exist, what the console will accept, who is online,
// and how to read the server's own settings file.
//
// Everything here is bounded and honest about not knowing. An upstream that
// cannot be reached produces "versions cannot be verified right now", never a
// substituted `latest` that nobody checked.
package gameserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Version is one upstream release of a game server.
type Version struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	ReleasedAt  time.Time `json:"releasedAt,omitempty"`
	Recommended bool      `json:"recommended,omitempty"`
	Latest      bool      `json:"latest,omitempty"`
}

// VersionList carries availability with the data. An empty list and a failed
// upstream read are different answers and must render differently.
type VersionList struct {
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	Source      string    `json:"source"`
	CheckedAt   time.Time `json:"checkedAt"`
	Versions    []Version `json:"versions"`
	Recommended string    `json:"recommended,omitempty"`
}

const (
	javaVersionManifest = "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json"
	maxVersionBytes     = 4 << 20
	versionCacheTTL     = 30 * time.Minute
	maxVersionsReturned = 60
)

var ErrUnsupportedGame = errors.New("unsupported game server")

type cacheEntry struct {
	list      VersionList
	expiresAt time.Time
}

// Adapter reads upstream version metadata through one bounded HTTP client and
// a short cache, so opening the wizard repeatedly does not hammer an upstream.
type Adapter struct {
	client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

func New() *Adapter {
	return &Adapter{
		client: &http.Client{Timeout: 10 * time.Second},
		now:    time.Now,
		cache:  map[string]cacheEntry{},
	}
}

// Versions answers for one blueprint id. An unknown id is an error; a known id
// whose upstream is unreachable is an unavailable list with a reason.
func (a *Adapter) Versions(ctx context.Context, blueprintID string) (VersionList, error) {
	switch blueprintID {
	case "minecraft-java":
		return a.cached(ctx, blueprintID, javaVersionManifest, a.javaVersions), nil
	case "minecraft-bedrock":
		// Mojang publishes no machine-readable Bedrock version index. Saying so
		// is the honest answer; scraping a download page and calling the result
		// a verified version would not be.
		return VersionList{
			Status: "unavailable", Source: "https://www.minecraft.net/en-us/download/server/bedrock",
			CheckedAt: a.now().UTC(), Versions: []Version{},
			Reason: "Mojang publishes no version index for Bedrock servers. The image resolves the current build at deployment and records it on the release.",
		}, nil
	default:
		return VersionList{}, fmt.Errorf("%w: %s", ErrUnsupportedGame, blueprintID)
	}
}

func (a *Adapter) cached(
	ctx context.Context,
	key, source string,
	load func(context.Context, string) (VersionList, error),
) VersionList {
	a.mu.Lock()
	entry, found := a.cache[key]
	a.mu.Unlock()
	if found && a.now().Before(entry.expiresAt) {
		return entry.list
	}
	list, err := load(ctx, source)
	if err != nil {
		// A previously successful read is better evidence than nothing, as long
		// as it is labelled as the cached answer it is.
		if found {
			stale := entry.list
			stale.Status = "stale"
			stale.Reason = "The upstream version list could not be refreshed. These are the versions read at " +
				stale.CheckedAt.Format(time.RFC3339) + "."
			return stale
		}
		return VersionList{
			Status: "unavailable", Source: source, CheckedAt: a.now().UTC(), Versions: []Version{},
			Reason: "Upstream versions cannot be verified right now. Choose an exact version you know exists, or try again.",
		}
	}
	a.mu.Lock()
	a.cache[key] = cacheEntry{list: list, expiresAt: a.now().Add(versionCacheTTL)}
	a.mu.Unlock()
	return list
}

func (a *Adapter) javaVersions(ctx context.Context, source string) (VersionList, error) {
	body, err := a.fetch(ctx, source)
	if err != nil {
		return VersionList{}, err
	}
	var manifest struct {
		Latest struct {
			Release  string `json:"release"`
			Snapshot string `json:"snapshot"`
		} `json:"latest"`
		Versions []struct {
			ID          string    `json:"id"`
			Type        string    `json:"type"`
			ReleaseTime time.Time `json:"releaseTime"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return VersionList{}, fmt.Errorf("version manifest is malformed: %w", err)
	}
	if manifest.Latest.Release == "" || len(manifest.Versions) == 0 {
		return VersionList{}, errors.New("version manifest named no release")
	}
	list := VersionList{
		Status: "available", Source: source, CheckedAt: a.now().UTC(),
		Versions: []Version{}, Recommended: manifest.Latest.Release,
	}
	for _, version := range manifest.Versions {
		// Only full releases are offered. Snapshots break clients and mods, and
		// a wizard that offers one by default is a wizard that breaks servers.
		if version.Type != "release" || version.ID == "" {
			continue
		}
		list.Versions = append(list.Versions, Version{
			ID: version.ID, Kind: version.Type, ReleasedAt: version.ReleaseTime.UTC(),
			Recommended: version.ID == manifest.Latest.Release,
			Latest:      version.ID == manifest.Latest.Release,
		})
	}
	sort.Slice(list.Versions, func(i, j int) bool {
		return list.Versions[i].ReleasedAt.After(list.Versions[j].ReleasedAt)
	})
	if len(list.Versions) > maxVersionsReturned {
		list.Versions = list.Versions[:maxVersionsReturned]
	}
	return list, nil
}

func (a *Adapter) fetch(ctx context.Context, source string) ([]byte, error) {
	if !strings.HasPrefix(source, "https://") {
		return nil, errors.New("version sources must be https")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version source answered %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, maxVersionBytes))
}
