package imagescan

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Trivy's vulnerability database is an OCI artifact: one gzip-compressed tar
// layer holding trivy.db and metadata.json. Trivy would fetch it itself, but
// only with a network, and a scanner with a network can reach whatever the
// host's bridge reaches. So this process — which already talks to registries
// for the deployments' update checks — fetches it, checks it against the
// digest its manifest names, and copies it into the scanner's volume. The
// scanner container then runs with no network at all.

// Source is one registry that serves the database.
type Source struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

func (s Source) String() string { return s.Registry + "/" + s.Repository + ":" + s.Tag }

// DefaultSources are Trivy's own, in Trivy's order: Google's mirror first,
// which needs no token and has no pull limit, then GitHub's registry.
var DefaultSources = []Source{
	{Registry: "mirror.gcr.io", Repository: "aquasec/trivy-db", Tag: "2"},
	{Registry: "ghcr.io", Repository: "aquasecurity/trivy-db", Tag: "2"},
}

const (
	databaseLayerType = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
	// The compressed layer is about 140 MB and the database inside it about
	// 1.6 GB; both bounds leave room for growth and stop a registry that
	// answers with something else from filling the disk.
	maxDatabaseLayer = 2 << 30
	maxDatabaseFile  = 8 << 30
	maxMetadataFile  = 64 << 10
	// DatabaseRetry is how long a failed download waits before the next
	// attempt, so a registry that is down is asked once an hour rather than
	// before every scan.
	DatabaseRetry = time.Hour
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// DatabaseState is what the scanner knows about the database in its volume,
// kept in a file beside the scans.
type DatabaseState struct {
	// Installed says the volume holds a complete database. It is false while
	// one is being copied in, and after a copy that did not finish.
	Installed bool `json:"installed"`
	// UpdatedAt is when the database was built — what its age is counted
	// from — and NextUpdate when its publisher says the next one is due.
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	NextUpdate   time.Time `json:"nextUpdate,omitempty"`
	DownloadedAt time.Time `json:"downloadedAt,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	Source       string    `json:"source,omitempty"`
	// LastAttemptAt and LastError are the most recent download's, whatever
	// came of it.
	LastAttemptAt time.Time `json:"lastAttemptAt,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
}

// Age is how old the database was at now; zero without one.
func (d DatabaseState) Age(now time.Time) time.Duration {
	if !d.Installed || d.UpdatedAt.IsZero() {
		return 0
	}
	return now.Sub(d.UpdatedAt)
}

// due reports whether a download should be attempted at now: there is no
// database, or its publisher's next one is due, and the last attempt did not
// fail within the retry interval.
func (d DatabaseState) due(now time.Time) bool {
	if !d.LastAttemptAt.IsZero() && d.LastError != "" && now.Sub(d.LastAttemptAt) < DatabaseRetry {
		return false
	}
	if !d.Installed {
		return true
	}
	if d.NextUpdate.IsZero() {
		return now.Sub(d.DownloadedAt) >= 24*time.Hour
	}
	return !now.Before(d.NextUpdate)
}

type databaseMetadata struct {
	Version    int       `json:"Version"`
	NextUpdate time.Time `json:"NextUpdate"`
	UpdatedAt  time.Time `json:"UpdatedAt"`
}

type ociManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Layers        []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// registryClient reads one artifact anonymously, answering a registry's
// bearer challenge with the token its realm hands out for a pull.
type registryClient struct {
	http   *http.Client
	source Source
	// base is the registry's address; https://<registry> unless a test
	// points it elsewhere.
	base  string
	token string
}

func (c *registryClient) get(ctx context.Context, path, accept string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && c.token == "" && attempt == 0 {
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if err := c.authorize(ctx, challenge); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%s answered %s", c.source, resp.Status)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("%s refused the pull token", c.source)
}

// authorize fetches a pull token from the realm a bearer challenge names. The
// realm must be https (or the test's own server), so a challenge cannot send
// this process to an address of its choosing over plain HTTP.
func (c *registryClient) authorize(ctx context.Context, challenge string) error {
	params := parseChallenge(challenge)
	realm := params["realm"]
	parsed, err := url.Parse(realm)
	if err != nil || realm == "" || (parsed.Scheme != "https" && !strings.HasPrefix(realm, c.base)) {
		return fmt.Errorf("%s asked for credentials this download does not have", c.source)
	}
	query := parsed.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	query.Set("scope", "repository:"+c.source.Repository+":pull")
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s's token service answered %s", c.source, resp.Status)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&token); err != nil {
		return fmt.Errorf("%s's token could not be read: %v", c.source, err)
	}
	c.token = token.Token
	if c.token == "" {
		c.token = token.AccessToken
	}
	if c.token == "" {
		return fmt.Errorf("%s handed out no token", c.source)
	}
	return nil
}

// parseChallenge reads `Bearer realm="…",service="…",scope="…"`.
func parseChallenge(header string) map[string]string {
	out := map[string]string{}
	scheme, rest, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return out
	}
	for _, part := range splitChallenge(rest) {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(name))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return out
}

// splitChallenge splits on the commas outside quotes: a scope may hold one.
func splitChallenge(value string) []string {
	parts := []string{}
	quoted, start := false, 0
	for i, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	return append(parts, value[start:])
}

// layer reads the artifact's manifest and returns the database layer's
// digest and size.
func (c *registryClient) layer(ctx context.Context) (string, int64, error) {
	resp, err := c.get(ctx, "/v2/"+c.source.Repository+"/manifests/"+c.source.Tag,
		"application/vnd.oci.image.manifest.v1+json")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var manifest ociManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&manifest); err != nil {
		return "", 0, fmt.Errorf("%s's manifest could not be read: %v", c.source, err)
	}
	for _, layer := range manifest.Layers {
		if layer.MediaType != databaseLayerType {
			continue
		}
		if !digestPattern.MatchString(layer.Digest) || layer.Size <= 0 || layer.Size > maxDatabaseLayer {
			return "", 0, fmt.Errorf("%s names a database layer this download will not take (%s, %d bytes)", c.source, layer.Digest, layer.Size)
		}
		return layer.Digest, layer.Size, nil
	}
	return "", 0, fmt.Errorf("%s's manifest has no vulnerability database layer", c.source)
}

// download writes the layer to path and checks it is the one the manifest
// named: the bytes are counted and hashed as they arrive, and a file that
// does not match is removed.
func (c *registryClient) download(ctx context.Context, digest string, size int64, path string) error {
	resp, err := c.get(ctx, "/v2/"+c.source.Repository+"/blobs/"+digest, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(resp.Body, size+1))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		err = fmt.Errorf("the download from %s stopped: %v", c.source, copyErr)
	case closeErr != nil:
		err = closeErr
	case written != size:
		err = fmt.Errorf("%s sent %d bytes of a %d-byte database", c.source, written, size)
	case "sha256:"+hex.EncodeToString(hasher.Sum(nil)) != digest:
		err = fmt.Errorf("the database from %s does not match the digest its manifest names", c.source)
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// repackDatabase reads the downloaded layer and writes the tar the volume
// is given: the two files Trivy reads, under db/, owned by root and readable
// by the account the scanner runs as. Any other entry is left out, so a
// layer cannot place a file anywhere else in the volume, and both files must
// be there.
func repackDatabase(layer io.Reader, out io.Writer) (databaseMetadata, error) {
	var metadata databaseMetadata
	gz, err := gzip.NewReader(layer)
	if err != nil {
		return metadata, fmt.Errorf("the database layer is not gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	tw := tar.NewWriter(out)
	now := time.Now().UTC()
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "db/", Mode: 0o755, ModTime: now}); err != nil {
		return metadata, err
	}
	found := map[string]bool{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return metadata, fmt.Errorf("the database layer could not be read: %v", err)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if header.Typeflag != tar.TypeReg || (name != "trivy.db" && name != "metadata.json") || found[name] {
			continue
		}
		limit := int64(maxDatabaseFile)
		if name == "metadata.json" {
			limit = maxMetadataFile
		}
		if header.Size < 0 || header.Size > limit {
			return metadata, fmt.Errorf("the database layer's %s is %d bytes, more than it can be", name, header.Size)
		}
		found[name] = true
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: "db/" + name, Mode: 0o644, Size: header.Size, ModTime: now,
		}); err != nil {
			return metadata, err
		}
		if name == "metadata.json" {
			raw, err := io.ReadAll(io.LimitReader(tr, header.Size))
			if err != nil {
				return metadata, err
			}
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return metadata, fmt.Errorf("the database's metadata could not be read: %v", err)
			}
			if _, err := tw.Write(raw); err != nil {
				return metadata, err
			}
			continue
		}
		if _, err := io.CopyN(tw, tr, header.Size); err != nil {
			return metadata, err
		}
	}
	if !found["trivy.db"] || !found["metadata.json"] {
		return metadata, errors.New("the database layer does not hold trivy.db and metadata.json")
	}
	if metadata.Version != 2 || metadata.UpdatedAt.IsZero() {
		return metadata, fmt.Errorf("the database is schema %d, built %s; this scanner reads schema 2", metadata.Version, metadata.UpdatedAt)
	}
	return metadata, tw.Close()
}

// readState reads the database's state file; a missing or unreadable one is
// no database.
func readState(dir string) DatabaseState {
	var state DatabaseState
	raw, err := os.ReadFile(filepath.Join(dir, "database.json"))
	if err != nil || json.Unmarshal(raw, &state) != nil {
		return DatabaseState{}
	}
	return state
}

func writeState(dir string, state DatabaseState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(dir, ".database.json.tmp")
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(dir, "database.json"))
}
