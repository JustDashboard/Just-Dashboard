// Package filecontext says what a folder or a file is to this server: a
// container's volume, a site's root, a Git checkout, covered by a backup or
// not, a deployment's source, a database's data directory, a unit file.
//
// It holds no inventory of its own and asks the machine nothing. The API reads
// what the Docker, Proxy, Backups, Git, Deployments, Databases and Processes
// pages already read (their held answers), turns each into Things — a fact and
// the paths it is about — and hands them here with the folder on screen. The
// matching is all in this file, so a new source is one more Thing builder in
// the API and nothing here.
//
// A fact is data, not prose: a kind, the names it is about, its state and the
// page that owns it. The page writes the sentence, so the wording lives in one
// place and is tested there.
package filecontext

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sources, which are also the pages a fact leads to.
const (
	SourceDocker    = "docker"
	SourceProxy     = "proxy"
	SourceBackups   = "backups"
	SourceGit       = "git"
	SourceDeploy    = "deploy"
	SourceDatabases = "databases"
	SourceProcesses = "processes"
	SourceDashboard = "dashboard"
)

// Kinds of fact. The frontend's sentence for each is in
// components/files/server-context.ts.
const (
	KindVolume           = "volume"            // a Docker volume's folder
	KindBind             = "bind"              // a container's bind mount
	KindCompose          = "compose"           // a Compose project's folder
	KindComposeFile      = "compose-file"      // a Compose project's file
	KindSiteRoot         = "site-root"         // a site serves files from here
	KindSiteConfig       = "site-config"       // a site's configuration file
	KindSiteLog          = "site-log"          // a site's access or error log
	KindCertificate      = "certificate"       // a certificate file, or its folder
	KindBackup           = "backup"            // an enabled or paused job covers it
	KindBackupTarget     = "backup-target"     // a job keeps its archives here
	KindNotBackedUp      = "not-backed-up"     // a folder that matters, covered by no enabled job
	KindBackupExcluded   = "backup-excluded"   // inside a covered folder but excluded from the job
	KindGit              = "git"               // a Git checkout
	KindDeploySource     = "deployment-source" // the checkout a deployment builds from
	KindDeployData       = "deployment-data"   // a folder a deployment keeps its data in
	KindDatabaseData     = "database-data"     // a database server's data directory or file
	KindDatabaseConfig   = "database-config"   // a database server's configuration file
	KindUnitFile         = "unit-file"         // a systemd unit's file
	KindPM2              = "pm2"               // a PM2 app's working folder
	KindPM2File          = "pm2-file"          // a PM2 app's script or log
	KindDashboardData    = "dashboard-data"    // Just Dashboard's own data
	KindDashboardInstall = "dashboard-install" // where Just Dashboard's stack is installed
)

// Backup states.
const (
	BackupOK     = "ok"
	BackupFailed = "failed"
	BackupPaused = "paused"
	BackupNever  = "never"
)

// Fact is one thing the dashboard knows about a path.
type Fact struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Names are what the fact is about, most specific first: the volume and
	// the containers using it, the site's domains, the job, the checkout.
	Names []string `json:"names"`
	// Product is the logo the fact is drawn with, where the server knows it;
	// Image is a container's image, from which the page picks one.
	Product string `json:"product,omitempty"`
	Image   string `json:"image,omitempty"`
	State   string `json:"state,omitempty"`
	// Detail is the one further word a kind has: a branch, a version, a
	// mount's destination.
	Detail   string     `json:"detail,omitempty"`
	Count    *int       `json:"count,omitempty"`
	At       *time.Time `json:"at,omitempty"`
	Until    *time.Time `json:"until,omitempty"`
	Schedule string     `json:"schedule,omitempty"`
	Target   string     `json:"target,omitempty"`
	Href     string     `json:"href,omitempty"`
	// From is the folder the fact is about when the path is inside it rather
	// than it: "inside the volume of shop-db".
	From string `json:"from,omitempty"`
	// Inherits says what is inside the path shares the fact.
	Inherits bool `json:"inherits,omitempty"`
}

// Thing is a fact and the resolved paths it is about.
type Thing struct {
	Fact  Fact
	Paths []string
	// Notable is a folder whose loss would matter: a volume, a checkout, a
	// site's root, a deployment's data. Only such a folder is said to be
	// "not backed up".
	Notable bool
}

// Job is a backup job as coverage needs it: what it archives, what it
// leaves out, and the fact that says so.
type Job struct {
	Fact     Fact
	Sources  []string
	Excludes []string
	Enabled  bool
}

// Child is one entry of the folder on screen: the path the listing calls it
// by, and what it resolves to (a symlink is matched as what it points at).
type Child struct {
	Path string
	Real string
}

// Input is everything one answer is made from.
type Input struct {
	Folder   string
	Children []Child
	Things   []Thing
	Jobs     []Job
	// Read names the sources that answered in time. A source not read is
	// left out of the answer, and "not backed up" is never said without the
	// backups.
	Read map[string]bool
	// Can says whether the reader may see a source's facts: whether they
	// could open its page.
	Can func(source string) bool
}

// Answer is what is known about the folder and each child.
type Answer struct {
	Path string `json:"path"`
	// Facts are the folder's own and those it inherits (From set).
	Facts []Fact `json:"facts"`
	// Children are each child's own facts, by the path the listing gives it.
	// What a child inherits from the folder is the folder's facts that
	// inherit; a child left out of a backup that covers the folder says so.
	Children map[string][]Fact `json:"children"`
}

// Within reports whether path is dir or inside it, by whole segments:
// /srv/app is not inside /srv/app2.
func Within(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// Resolve answers for the folder and its children.
func Resolve(in Input) Answer {
	can := in.Can
	if can == nil {
		can = func(string) bool { return true }
	}
	visible := func(source string) bool { return in.Read[source] && can(source) }

	// Exact paths to the things about them: one lookup per child and one per
	// ancestor of the folder, whatever the size of the inventories.
	index := map[string][]int{}
	for i, t := range in.Things {
		if !visible(t.Fact.Source) {
			continue
		}
		for _, p := range t.Paths {
			p = clean(p)
			if p == "" {
				continue
			}
			index[p] = appendOnce(index[p], i)
		}
	}
	var jobs []Job
	if visible(SourceBackups) {
		jobs = in.Jobs
	}

	folder := clean(in.Folder)
	out := Answer{Path: folder, Facts: []Fact{}, Children: map[string][]Fact{}}

	// The folder: its own facts, then each ancestor's that inherit, nearest
	// first.
	// A thing with two paths on the way up (a volume's folder and its _data)
	// is said once, at the nearest.
	notable := false
	said := map[int]bool{}
	for _, i := range index[folder] {
		t := in.Things[i]
		said[i] = true
		out.Facts = append(out.Facts, t.Fact)
		notable = notable || t.Notable
	}
	for dir := parent(folder); dir != ""; dir = parent(dir) {
		for _, i := range index[dir] {
			t := in.Things[i]
			if !t.Fact.Inherits || said[i] {
				continue
			}
			said[i] = true
			f := t.Fact
			f.From = dir
			out.Facts = append(out.Facts, f)
			notable = notable || t.Notable
		}
	}
	out.Facts = append(out.Facts, coverage(jobs, folder, notable, visible(SourceBackups))...)

	// Each child: its own facts, and its coverage where that differs from the
	// folder's.
	folderCovered := covered(jobs, folder) != nil
	for _, c := range in.Children {
		real := clean(c.Real)
		if real == "" {
			real = clean(c.Path)
		}
		var facts []Fact
		childNotable := false
		for _, i := range index[real] {
			t := in.Things[i]
			facts = append(facts, t.Fact)
			childNotable = childNotable || t.Notable
		}
		own := coverage(jobs, real, childNotable, visible(SourceBackups))
		if folderCovered && Within(folder, real) {
			// The folder's coverage speaks for the child, which says only
			// what differs: a job of its own, or being left out.
			if covered(jobs, real) == nil {
				if excluded := excludedBy(jobs, real); excluded != nil {
					facts = append(facts, *excluded)
				}
			}
			for _, f := range own {
				if f.Kind == KindBackup && f.From == "" {
					facts = append(facts, f)
				}
			}
		} else {
			// Not covered through the folder (or a symlink to somewhere
			// else): its coverage is its own to say.
			facts = append(facts, own...)
		}
		if len(facts) > 0 {
			out.Children[c.Path] = sortFacts(facts)
		}
	}
	out.Facts = sortFacts(out.Facts)
	return out
}

// coverage is the backup fact for a path: every job that covers it, or "not
// backed up" for a folder that matters which none covers.
func coverage(jobs []Job, path string, notable, read bool) []Fact {
	var out []Fact
	for _, j := range jobs {
		source := coveringSource(j, path)
		if source == "" {
			continue
		}
		f := j.Fact
		f.Inherits = true
		if source != path {
			f.From = source
		}
		if !j.Enabled {
			f.State = BackupPaused
		}
		out = append(out, f)
	}
	enabled := false
	for _, f := range out {
		enabled = enabled || f.State != BackupPaused
	}
	if !enabled && notable && read {
		out = append(out, notBackedUp())
	}
	return out
}

func notBackedUp() Fact {
	return Fact{Kind: KindNotBackedUp, Source: SourceBackups, Names: []string{}, Href: "/backups"}
}

// covered is the first job whose sources hold the path and whose excludes
// do not.
func covered(jobs []Job, path string) *Job {
	for i := range jobs {
		if coveringSource(jobs[i], path) != "" {
			return &jobs[i]
		}
	}
	return nil
}

func coveringSource(j Job, path string) string {
	best := ""
	for _, s := range j.Sources {
		s = clean(s)
		if Within(s, path) && len(s) > len(best) {
			best = s
		}
	}
	if best == "" {
		return ""
	}
	for _, e := range j.Excludes {
		if e = clean(e); Within(e, path) {
			return ""
		}
	}
	return best
}

// excludedBy is the fact for a path a covering job leaves out on purpose.
func excludedBy(jobs []Job, path string) *Fact {
	for _, j := range jobs {
		for _, s := range j.Sources {
			if !Within(clean(s), path) {
				continue
			}
			for _, e := range j.Excludes {
				if Within(clean(e), path) {
					f := j.Fact
					f.Kind = KindBackupExcluded
					f.Inherits = true
					return &f
				}
			}
		}
	}
	return nil
}

// sortFacts keeps a stable order for the page: own facts before inherited,
// then by kind and first name. Which one a row shows is the page's choice.
func sortFacts(facts []Fact) []Fact {
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if (a.From == "") != (b.From == "") {
			return a.From == ""
		}
		if len(a.From) != len(b.From) {
			return len(a.From) > len(b.From)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return first(a.Names) < first(b.Names)
	})
	return facts
}

func first(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func clean(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

func parent(p string) string {
	if p == "/" || p == "" {
		return ""
	}
	return filepath.Dir(p)
}

func appendOnce(list []int, i int) []int {
	for _, have := range list {
		if have == i {
			return list
		}
	}
	return append(list, i)
}
