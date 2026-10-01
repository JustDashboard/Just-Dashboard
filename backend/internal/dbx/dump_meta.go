package dbx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// What is known about a dump besides its bytes.
//
// A dump directory is a list of files with names and sizes. The questions
// asked of one — is this the schema-only one, which tool wrote it, how long
// did it take, was it the copy taken before last week's restore — are answered
// by nothing in a file's name, and on some engines by nothing in the file.
//
// So each dump has a small file beside it. Beside it rather than in the
// dashboard's own database, because the dump directory is what gets copied
// off the machine, and a dump whose description lives somewhere else arrives
// without one.

// dumpMetaSuffix is appended to the dump's own name.
const dumpMetaSuffix = ".meta.json"

// dumpMetaVersion is the layout of the file, for a reader that meets a newer
// one.
const dumpMetaVersion = 1

// The ways a dump comes to be in the directory.
const (
	DumpOriginDump   = "dump"
	DumpOriginUpload = "upload"
	// DumpOriginSafety is the copy taken before a restore replaced the
	// database it is of.
	DumpOriginSafety = "safety"
)

// DumpContents is what a dump was asked to hold.
type DumpContents struct {
	SchemaOnly    bool     `json:"schemaOnly,omitempty"`
	DataOnly      bool     `json:"dataOnly,omitempty"`
	Tables        []string `json:"tables,omitempty"`
	ExcludeTables []string `json:"excludeTables,omitempty"`
	Compression   string   `json:"compression,omitempty"`
	// Databases is the numbered databases a Redis dump covers.
	Databases []int `json:"databases,omitempty"`
}

// ContentsOf records the options a dump was taken with.
func ContentsOf(opts DumpOptions) DumpContents {
	return DumpContents{
		SchemaOnly: opts.SchemaOnly, DataOnly: opts.DataOnly,
		Tables: opts.Tables, ExcludeTables: opts.ExcludeTables,
		Compression: opts.Compression, Databases: opts.RedisDatabases,
	}
}

// DumpMeta describes one dump.
type DumpMeta struct {
	Version    int       `json:"version"`
	File       string    `json:"file"`
	Connection string    `json:"connection,omitempty"`
	Driver     Driver    `json:"driver,omitempty"`
	Database   string    `json:"database,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
	Size       int64     `json:"size"`
	// Tool and ToolVersion are what wrote it: the engine's own tool and its
	// version, or the dashboard's dumper and the dashboard's.
	Tool        string       `json:"tool,omitempty"`
	ToolVersion string       `json:"toolVersion,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	Contents    DumpContents `json:"contents"`
	Note        string       `json:"note,omitempty"`
	Origin      string       `json:"origin"`
	// By is the account that asked for it.
	By string `json:"by,omitempty"`
}

// MetaOf builds the description of a dump that has just been taken.
func MetaOf(res *DumpResult, opts DumpOptions) DumpMeta {
	meta := DumpMeta{
		Version: dumpMetaVersion, File: res.File, Driver: res.Driver, Database: res.Database,
		StartedAt: res.StartedAt, Size: res.Size, Tool: res.Tool, ToolVersion: res.ToolVersion,
		Summary: res.Summary, Contents: ContentsOf(opts), Origin: DumpOriginDump,
	}
	if d, err := time.ParseDuration(res.Duration); err == nil {
		meta.DurationMs = d.Milliseconds()
	}
	return meta
}

// DumpMetaPath is where a dump's description is kept.
func DumpMetaPath(dumpPath string) string { return dumpPath + dumpMetaSuffix }

// IsDumpMetaFile reports a name that is a description rather than a dump, so
// a listing of dumps does not offer one for restore.
func IsDumpMetaFile(name string) bool { return strings.HasSuffix(name, dumpMetaSuffix) }

// WriteDumpMeta writes a dump's description beside it. It is written to a
// temporary file and renamed, so a reader never finds half of one.
func WriteDumpMeta(dumpPath string, meta DumpMeta) error {
	meta.Version = dumpMetaVersion
	if meta.File == "" {
		meta.File = filepath.Base(dumpPath)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dumpPath), ".jd-meta-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, DumpMetaPath(dumpPath))
}

// ReadDumpMeta returns a dump's description, or nil when it has none: a file
// that was put in the directory by hand, or written before descriptions were.
// One that cannot be read is treated the same way — it describes a dump, and
// the dump is still there to be listed.
func ReadDumpMeta(dumpPath string) *DumpMeta {
	data, err := os.ReadFile(DumpMetaPath(dumpPath))
	if err != nil {
		return nil
	}
	var meta DumpMeta
	if err := json.Unmarshal(data, &meta); err != nil || meta.Version > dumpMetaVersion {
		return nil
	}
	return &meta
}

// RemoveDumpMeta deletes a dump's description, for when the dump goes.
func RemoveDumpMeta(dumpPath string) error {
	err := os.Remove(DumpMetaPath(dumpPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// DumpKind names what a dump file is from its first bytes, for the listing.
// The name on a file is whatever the last person to move it chose.
func DumpKind(path string) string {
	switch dumpFormatOf(path) {
	case dumpFormatSQLText:
		if strings.HasSuffix(path, ".gz") {
			return "compressed SQL"
		}
		return "SQL"
	case dumpFormatArchive:
		return "JSON Lines"
	case dumpFormatForeignSQL:
		return "SQL"
	case dumpFormatNativeGzip:
		if strings.HasSuffix(path, ".sql.gz") {
			return "compressed SQL"
		}
		return "archive"
	}
	head := make([]byte, len(sqliteMagic))
	if f, err := os.Open(path); err == nil {
		n, _ := f.Read(head)
		f.Close()
		switch {
		case string(head[:n]) == sqliteMagic:
			return "SQLite file"
		case n >= 5 && string(head[:5]) == "PGDMP":
			return "pg_dump archive"
		}
	}
	return "archive"
}
