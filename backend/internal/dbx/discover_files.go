package dbx

import (
	"bytes"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Databases that are a file, and data that has lost its server.
//
// The commonest database nobody listed is a SQLite file in an application's
// volume. It has no port, no process and no image that names it; the only
// thing that says what it is is the file itself. So a file is recognised by
// its first bytes, which the format fixes, and by nothing else — an extension
// is only a reason to look.
//
// Looking means reading sixteen bytes. A file is never opened with the SQLite
// driver to find out whether it is one: the driver takes locks and creates
// -wal and -shm files beside what it opens, and doing that to another
// application's database in order to list it is not a read.

// FileFacts is one database file the scan confirmed.
type FileFacts struct {
	Path string
	// Engine is what the first bytes said: sqlite or duckdb.
	Engine   string
	Size     int64
	Modified time.Time
	WAL      bool
}

// DataDirFacts is a directory holding an engine's data, recognised by the
// file every such directory has.
type DataDirFacts struct {
	Path string
	// Engine is the product whose marker was found, and Version what the
	// marker says where it says one (PG_VERSION does).
	Engine   string
	Version  string
	Modified time.Time
}

// EmbeddedFile is a database file found in a container's own writable layer,
// by its path inside the container.
type EmbeddedFile struct {
	Container string
	Path      string
	Engine    string
	Size      int64
	Modified  time.Time
}

// MagicLen is how much of a file FileMagic needs.
const MagicLen = 16

var sqliteMagic = []byte("SQLite format 3\x00")

// FileMagic names the database format a file starts with.
func FileMagic(header []byte) (engine string, ok bool) {
	if len(header) >= len(sqliteMagic) && bytes.Equal(header[:len(sqliteMagic)], sqliteMagic) {
		return "sqlite", true
	}
	// DuckDB keeps a checksum in its first eight bytes and its name after.
	if len(header) >= 12 && string(header[8:12]) == "DUCK" {
		return "duckdb", true
	}
	return "", false
}

var fileExtensions = map[string]bool{
	".db": true, ".sqlite": true, ".sqlite3": true, ".db3": true, ".s3db": true, ".sl3": true,
	".duckdb": true, ".ddb": true,
}

// FileCandidate reports a name worth reading the first bytes of. It bounds
// how many files a scan opens; it decides nothing about what a file is.
func FileCandidate(name string) bool {
	return fileExtensions[strings.ToLower(filepath.Ext(name))]
}

// DataDirMarker recognises an engine's data directory from the names in it,
// returning the product and the file that carries its version, if one does.
//
// A directory recognised here is recorded and not walked into, so being wrong
// costs more than a mislabel: every database file below it goes unlisted. No
// engine is therefore taken on one name alone where a second can be asked for:
// PostgreSQL's version file beside its base directory, InnoDB's system
// tablespace beside the mysql schema every initialised MySQL and MariaDB has.
// WiredTiger's files are called what nothing else is. Redis has no second name
// to ask for — its whole footprint is a dump.rdb, and a dump.rdb is also what
// a redis-server once run from an application's directory leaves there. So a
// directory is Redis's only when everything in it is: one stray dump beside an
// application's own files does not make the application a Redis.
func DataDirMarker(names map[string]bool) (engine, versionFile string, ok bool) {
	switch {
	case names["PG_VERSION"] && (names["base"] || names["global"]):
		return "postgres", "PG_VERSION", true
	case names["mysql"] && (names["ibdata1"] || names["ib_buffer_pool"]):
		return "mysql", "", true
	case names["WiredTiger"] || names["WiredTiger.wt"]:
		return "mongodb", "", true
	case (names["dump.rdb"] || names["appendonlydir"]) && onlyRedisFiles(names):
		return "redis", "", true
	}
	return "", "", false
}

// redisFiles are what a Redis, a Valkey or a KeyDB writes into its working
// directory, and the configuration people keep beside it.
var redisFiles = map[string]bool{
	"dump.rdb": true, "appendonlydir": true, "appendonly.aof": true, "nodes.conf": true,
	"redis.conf": true, "valkey.conf": true, "keydb.conf": true, "users.acl": true, "lost+found": true,
}

func onlyRedisFiles(names map[string]bool) bool {
	for name := range names {
		// temp-<pid>.rdb and temp-rewriteaof-<pid>.aof are a save in progress.
		temporary := strings.HasPrefix(name, "temp-") && (strings.HasSuffix(name, ".rdb") || strings.HasSuffix(name, ".aof"))
		if !redisFiles[name] && !temporary {
			return false
		}
	}
	return true
}

var scanSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, ".hg": true, "vendor": true, "__pycache__": true,
	".cache": true, ".next": true, ".npm": true, ".pnpm-store": true, ".yarn": true, ".bun": true,
	".cargo": true, ".rustup": true, ".nvm": true, ".gradle": true, ".m2": true, ".venv": true, "venv": true,
	"site-packages": true, "lost+found": true, "proc": true, "sys": true, "dev": true, "run": true,
	"overlay2": true, "snap": true, ".Trash": true, "Trash": true, ".terraform": true, "go": true,
}

// SkipScanDir reports a directory a database scan does not go into: package
// caches and build output, which hold thousands of files and no database an
// operator runs.
func SkipScanDir(name string) bool { return scanSkipDirs[name] }

// Places says whose each part of the filesystem is, so a file can be listed
// with its owner instead of as a bare path.
type Places struct {
	// StorePath is the dashboard's own database, and DataDir where it keeps
	// the rest of its state.
	StorePath string
	DataDir   string
	Mounts    []MountPlace
	// VolumesDir is where Docker's local driver keeps volumes, so a file in a
	// volume no container mounts any more is still known to be in one.
	VolumesDir string
	// Deployments and Stacks are the directories deployment projects and
	// compose stacks live in.
	Deployments []ProjectPlace
	Stacks      []ProjectPlace
}

// MountPlace is one directory mounted into containers.
type MountPlace struct {
	Source     string
	Volume     string
	Containers []string
}

// ProjectPlace is a named directory.
type ProjectPlace struct {
	Name string
	Path string
}

func within(dir, file string) bool {
	if dir == "" || dir == "/" {
		return false
	}
	return file == dir || strings.HasPrefix(file, strings.TrimSuffix(dir, "/")+"/")
}

// HostView strips the prefix a containerised dashboard sees the host's root
// under, so /host/var/lib/x and /var/lib/x are judged the same.
func HostView(p string) string {
	if rest, ok := strings.CutPrefix(p, "/host/"); ok {
		return "/" + rest
	}
	return p
}

// holderOf says whose a path is, most specific owner first.
func holderOf(file string, places Places) (holder string, mount *MountPlace, project string) {
	view := HostView(file)
	if places.StorePath != "" && (file == places.StorePath || view == places.StorePath) {
		return HolderSelf, nil, ""
	}
	var best *MountPlace
	for i := range places.Mounts {
		m := &places.Mounts[i]
		if (within(m.Source, file) || within(m.Source, view)) && (best == nil || len(m.Source) > len(best.Source)) {
			best = m
		}
	}
	if best != nil {
		return HolderContainer, best, ""
	}
	if within(places.VolumesDir, file) && file != places.VolumesDir {
		// A volume nothing mounts: what a removed container left behind.
		name, _, _ := strings.Cut(strings.TrimPrefix(file, strings.TrimSuffix(places.VolumesDir, "/")+"/"), "/")
		return HolderContainer, &MountPlace{Source: path.Join(places.VolumesDir, name, "_data"), Volume: name}, ""
	}
	if within(places.DataDir, file) || within(places.DataDir, view) {
		return HolderSystem, nil, ""
	}
	for _, d := range places.Deployments {
		if within(d.Path, file) || within(d.Path, view) {
			return HolderDeployment, nil, d.Name
		}
	}
	for _, s := range places.Stacks {
		if within(s.Path, file) || within(s.Path, view) {
			return HolderCompose, nil, s.Name
		}
	}
	for _, segment := range strings.Split(path.Dir(view), "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			// A program's own state under somebody's home: a browser profile,
			// an editor's index, a CLI's history.
			return HolderTool, nil, ""
		}
		if segment == "nssdb" {
			return HolderSystem, nil, ""
		}
	}
	for _, system := range []string{"/var/lib", "/var/cache", "/usr", "/etc", "/var/spool", "/snap"} {
		if within(system, view) {
			return HolderSystem, nil, ""
		}
	}
	return HolderApplication, nil, ""
}

// discoverFiles adds the database files the scan confirmed, and the data
// directories that have no server.
func discoverFiles(files []FileFacts, dirs []DataDirFacts, places Places, inv *Inventory) {
	servedContainers := map[string]bool{}
	servedDirs := map[string]bool{}
	for _, inst := range inv.Instances {
		if inst.Container != nil {
			servedContainers[inst.Container.Name] = true
		}
		if inst.Host != nil && inst.Host.DataDir != "" {
			servedDirs[inst.Host.DataDir] = true
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		p := productByID[f.Engine]
		if p == nil || f.Path == "" || seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		holder, mount, project := holderOf(f.Path, places)
		inst := Instance{
			Key: "file:" + f.Path, Kind: KindFile, Name: path.Base(f.Path),
			Source: SourceFile, State: StateFile, Confidence: ConfidenceMagic,
			Endpoints: []Endpoint{{Kind: "file", Path: f.Path, Primary: true}},
			File:      &FileRef{Path: f.Path, Size: f.Size, Modified: f.Modified, WAL: f.WAL, Holder: holder, Project: project},
			Evidence:  []string{"the file begins with " + p.label + "'s signature"},
		}
		describe(&inst, p)
		if mount != nil {
			inst.File.Volume, inst.File.Containers = mount.Volume, mount.Containers
		}
		if p.driver != "" {
			inst.Database, inst.Credentials = f.Path, CredentialsOpen
		} else {
			inst.Credentials = CredentialsUnknown
		}
		if holder == HolderSelf {
			inst.Self = true
			inst.Reason = "this is the dashboard's own store — it is listed, and never connected"
		}
		inv.Instances = append(inv.Instances, inst)
	}
	for _, d := range dirs {
		p := productByID[d.Engine]
		if p == nil || d.Path == "" || seen[d.Path] {
			continue
		}
		seen[d.Path] = true
		if servedDirs[d.Path] || servedDirs[HostView(d.Path)] {
			continue
		}
		holder, mount, project := holderOf(d.Path, places)
		if mount != nil && servedBy(mount, servedContainers) {
			// The volume of a database container that is listed already: this
			// is that server's data, not data without one.
			continue
		}
		inst := Instance{
			Key: "data:" + d.Path, Kind: KindData, Name: path.Base(d.Path),
			Version: cleanVersion(d.Version), Source: SourceFile, State: StateData,
			Confidence: ConfidenceMarker, Credentials: CredentialsUnknown,
			Endpoints: []Endpoint{{Kind: "file", Path: d.Path}},
			File:      &FileRef{Path: d.Path, Modified: d.Modified, Holder: holder, Project: project},
			Evidence:  []string{"the directory holds the files a " + p.label + " server keeps its data in"},
			Reason:    "a " + p.label + " data directory with no server running on it",
		}
		describe(&inst, p)
		// A directory is not something a driver opens: it needs its server.
		inst.Driver = ""
		if mount != nil {
			inst.Source = SourceVolume
			inst.File.Volume, inst.File.Containers = mount.Volume, mount.Containers
			if mount.Volume != "" {
				inst.Name = mount.Volume
			}
		}
		inv.Instances = append(inv.Instances, inst)
	}
}

func servedBy(mount *MountPlace, served map[string]bool) bool {
	for _, name := range mount.Containers {
		if served[name] {
			return true
		}
	}
	return false
}

// discoverEmbedded lists the database files applications keep inside their
// own containers.
//
// These are the databases most likely to be lost: an application started with
// no volume writes its SQLite file into the container's writable layer, where
// it survives a restart and not a recreate. Nothing outside the container can
// open such a file, so it is listed with that said, and never connected.
func discoverEmbedded(list []EmbeddedFile, containers []ContainerFacts, inv *Inventory) {
	byName := map[string]*ContainerFacts{}
	for i := range containers {
		byName[containers[i].Name] = &containers[i]
	}
	seen := map[string]bool{}
	for _, f := range list {
		p := productByID[f.Engine]
		key := "embedded:" + f.Container + ":" + f.Path
		if p == nil || f.Container == "" || f.Path == "" || seen[key] {
			continue
		}
		seen[key] = true
		inst := Instance{
			Key: key, Kind: KindEmbedded, Name: path.Base(f.Path),
			Source: SourceDocker, State: StateFile, Confidence: ConfidenceMagic, Credentials: CredentialsUnknown,
			Endpoints: []Endpoint{{Kind: "file", Path: f.Path}},
			Container: &ContainerRef{Name: f.Container},
			File: &FileRef{
				Path: f.Path, Size: f.Size, Modified: f.Modified,
				Holder: HolderContainer, Containers: []string{f.Container},
			},
			Evidence: []string{
				"the file begins with " + p.label + "'s signature",
				"it is in " + f.Container + "'s writable layer, which no volume holds",
			},
			Reason: "it is kept inside " + f.Container + " itself, not in a volume — nothing outside the container can open it, and removing the container deletes it",
		}
		describe(&inst, p)
		// Nothing here can reach a file in another filesystem's layer.
		inst.Driver = ""
		if c := byName[f.Container]; c != nil {
			inst.State = c.State
			inst.Container = &ContainerRef{
				ID: c.ID, Name: c.Name, Image: c.Image, Status: c.Status, Health: c.Health,
				ComposeProject: c.Labels[labelComposeProject], ComposeService: c.Labels[labelComposeService],
				EnvironmentID: c.Labels[labelEnvironmentID], DataVolumes: []DataVolume{},
			}
		}
		inv.Instances = append(inv.Instances, inst)
	}
}
