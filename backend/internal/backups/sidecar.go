package backups

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
)

// Every archive is written with its manifest beside it, at the archive's own
// name plus ".manifest.json", so a destination says what it holds without the
// database of the server that wrote it: which install, job and run took each
// archive, when, how large it is, its digest, what kinds of thing it covers
// and which key it was sealed to. A fresh server reads these to list what it
// could import (catalogue.go), and imports an archive from them.
//
// The archive itself cannot carry its manifest — the manifest's digest is
// over the archive's bytes — and archives written before 0.8.2 have nothing
// beside them, so those are listed by name and size and are not imported.
//
// An encrypted archive's manifest is sealed to the same key, because its
// source paths, database names and paused containers say what the archive
// holds; what stays readable is who took it and when, its size and digest,
// how many of each kind it covers, and the public key that opens it. The
// digest proves that an archive is the one its manifest describes and that it
// arrived whole. It does not prove who wrote the pair: anyone who can write to
// the destination can write both, which is why an import is an administrator's
// act and a restore from it is confirmed like any other.

const (
	sidecarSuffix   = ".manifest.json"
	sidecarFormat   = "just-dashboard-backup-manifest"
	maxSidecarBytes = 1 << 20
)

// ContentSummary counts what an archive covers, by kind.
type ContentSummary struct {
	Paths           int `json:"paths"`
	Volumes         int `json:"volumes"`
	DatabaseDumps   int `json:"databaseDumps"`
	SQLiteSnapshots int `json:"sqliteSnapshots"`
}

func summarise(m *Manifest) ContentSummary {
	var out ContentSummary
	for _, source := range m.Sources {
		if _, ok := source.SourceVolume(); ok {
			out.Volumes++
		} else {
			out.Paths++
		}
	}
	out.DatabaseDumps, out.SQLiteSnapshots = len(m.DatabaseDumps), len(m.SQLiteSnapshots)
	return out
}

// Kinds names what a summary covers, in the words the catalogue groups by.
func (c ContentSummary) Kinds() []string {
	out := []string{}
	if c.Paths > 0 {
		out = append(out, "paths")
	}
	if c.Volumes > 0 {
		out = append(out, "volume")
	}
	if c.DatabaseDumps > 0 {
		out = append(out, "dump")
	}
	if c.SQLiteSnapshots > 0 {
		out = append(out, "snapshot")
	}
	return out
}

// SidecarOrigin is the readable part of an archive's origin.
type SidecarOrigin struct {
	ID        string    `json:"id"`
	Host      string    `json:"host,omitempty"`
	JobID     int64     `json:"jobId"`
	JobName   string    `json:"jobName"`
	RunID     int64     `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
}

// SidecarEncryption names the key an archive was sealed to, by its public half.
type SidecarEncryption struct {
	Format    string `json:"format"`
	Recipient string `json:"recipient"`
}

// Sidecar is the file written beside an archive.
type Sidecar struct {
	Format         string             `json:"format"`
	Version        int                `json:"version"`
	Artifact       string             `json:"artifact"`
	ArtifactDigest string             `json:"artifactDigest"`
	ArtifactBytes  int64              `json:"artifactBytes"`
	Origin         SidecarOrigin      `json:"origin"`
	Contents       ContentSummary     `json:"contents"`
	Encryption     *SidecarEncryption `json:"encryption,omitempty"`
	// Manifest is the run's manifest as the server that took it recorded it;
	// SealedManifest is the same, age-encrypted to the archive's key and
	// base64-encoded, for an encrypted archive.
	Manifest       *Manifest `json:"manifest,omitempty"`
	SealedManifest string    `json:"sealedManifest,omitempty"`
}

// newSidecar describes a finished archive. An encrypted one's manifest is
// sealed to the key the archive was.
func newSidecar(name string, manifest *Manifest, seal *archiveSeal) ([]byte, error) {
	if manifest.Origin == nil {
		return nil, errors.New("the manifest has no origin")
	}
	origin := manifest.Origin
	sidecar := Sidecar{
		Format: sidecarFormat, Version: 1, Artifact: name,
		ArtifactDigest: manifest.ArtifactDigest, ArtifactBytes: manifest.ArtifactBytes,
		Origin: SidecarOrigin{ID: origin.ID, Host: origin.Host, JobID: origin.JobID, JobName: origin.JobName,
			RunID: origin.RunID, StartedAt: origin.StartedAt},
		Contents: summarise(manifest),
	}
	plain, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if seal == nil {
		sidecar.Manifest = manifest
	} else {
		sidecar.Encryption = &SidecarEncryption{Format: archiveFormatAge, Recipient: seal.recipient.String()}
		var sealed bytes.Buffer
		writer, err := age.Encrypt(&sealed, seal.recipient)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(plain); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		sidecar.SealedManifest = base64.StdEncoding.EncodeToString(sealed.Bytes())
	}
	out, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// parseSidecar reads a file beside an archive, refusing one that is not a
// manifest this dashboard wrote or that disagrees with itself.
func parseSidecar(raw []byte, artifact string) (*Sidecar, error) {
	if len(raw) > maxSidecarBytes {
		return nil, errors.New("the manifest beside the archive is larger than a manifest is")
	}
	var sidecar Sidecar
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&sidecar); err != nil {
		return nil, fmt.Errorf("the manifest beside the archive is not readable: %w", err)
	}
	switch {
	case sidecar.Format != sidecarFormat:
		return nil, errors.New("the file beside the archive is not a Just Dashboard manifest")
	case sidecar.Version != 1:
		return nil, fmt.Errorf("the manifest beside the archive is version %d, which this dashboard does not read", sidecar.Version)
	case sidecar.Artifact != artifact:
		return nil, errors.New("the manifest beside the archive names another archive")
	case !validSHA256(sidecar.ArtifactDigest):
		return nil, errors.New("the manifest beside the archive has no usable digest")
	case strings.TrimSpace(sidecar.Origin.ID) == "" || sidecar.Origin.RunID <= 0:
		return nil, errors.New("the manifest beside the archive does not say where it was taken")
	case (sidecar.Manifest == nil) == (sidecar.SealedManifest == ""):
		return nil, errors.New("the manifest beside the archive holds neither or both of a clear and a sealed manifest")
	case sidecar.SealedManifest != "" && (sidecar.Encryption == nil || sidecar.Encryption.Format != archiveFormatAge || sidecar.Encryption.Recipient == ""):
		return nil, errors.New("the manifest beside the archive is sealed but does not say to which key")
	}
	return &sidecar, nil
}

// openSidecar answers the run manifest a sidecar carries, opening a sealed one
// with the key it names, and checks it says what the readable half says.
func (s *Store) openSidecar(ctx context.Context, sidecar *Sidecar) (*Manifest, error) {
	manifest := sidecar.Manifest
	if sidecar.SealedManifest != "" {
		identity, err := s.identityFor(ctx, sidecar.Encryption.Recipient)
		if err != nil {
			return nil, err
		}
		sealed, err := base64.StdEncoding.DecodeString(sidecar.SealedManifest)
		if err != nil {
			return nil, errors.New("the sealed manifest beside the archive is not base64")
		}
		reader, err := age.Decrypt(bytes.NewReader(sealed), identity)
		if err != nil {
			return nil, fmt.Errorf("the sealed manifest beside the archive could not be opened: %w", err)
		}
		plain, err := io.ReadAll(io.LimitReader(reader, maxSidecarBytes+1))
		if err != nil {
			return nil, fmt.Errorf("the sealed manifest beside the archive is damaged: %w", err)
		}
		if len(plain) > maxSidecarBytes {
			return nil, errors.New("the sealed manifest beside the archive is larger than a manifest is")
		}
		manifest = &Manifest{}
		if err := json.Unmarshal(plain, manifest); err != nil {
			return nil, fmt.Errorf("the sealed manifest beside the archive is not readable: %w", err)
		}
	}
	switch {
	case manifest.Version != 1 || !manifest.Complete:
		return nil, errors.New("the archive's manifest is not a complete one")
	case manifest.ArtifactDigest != sidecar.ArtifactDigest:
		return nil, errors.New("the archive's manifest and the file beside it disagree about its digest")
	case manifest.Origin == nil || manifest.Origin.ID != sidecar.Origin.ID || manifest.Origin.RunID != sidecar.Origin.RunID:
		return nil, errors.New("the archive's manifest and the file beside it disagree about where it was taken")
	case (manifest.Encryption == nil) != (sidecar.Encryption == nil):
		return nil, errors.New("the archive's manifest and the file beside it disagree about its encryption")
	case manifest.Encryption != nil && manifest.Encryption.Recipient != sidecar.Encryption.Recipient:
		return nil, errors.New("the archive's manifest and the file beside it disagree about its key")
	}
	return manifest, nil
}
