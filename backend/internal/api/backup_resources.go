package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// backupContainerPauser is the Docker owner's side of a job's paused
// containers: a pause and an unpause by name or id, nothing else. Backups
// decides when; Docker decides how.
type backupContainerPauser struct {
	docker *dockerx.Client
}

func (p *backupContainerPauser) PauseContainer(ctx context.Context, nameOrID string) error {
	return p.docker.Lifecycle(ctx, nameOrID, dockerx.ActionPause, nil)
}

func (p *backupContainerPauser) UnpauseContainer(ctx context.Context, nameOrID string) error {
	return p.docker.Lifecycle(ctx, nameOrID, dockerx.ActionUnpause, nil)
}

// backupResource is one thing on this server the dashboard already knows
// about and a backup could protect: a Docker volume, a compose stack, a
// deployment's checkout, a Git repository, a saved database, the proxy's
// configuration, or the dashboard's own data. Each carries the job the
// operator would write for it, so protecting it is one press rather than a
// form filled from memory.
type backupResource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	// Paths are the directories on disk a file job would have to include.
	// A database dumped natively has none.
	Paths        []string         `json:"paths,omitempty"`
	ConnectionID int64            `json:"connectionId,omitempty"`
	Suggest      backupSuggestion `json:"suggest"`
	CoveredBy    []backupCoverer  `json:"coveredBy"`
	// Protected means an enabled job covers it — every path of it, or its
	// native dump. A paused job is listed under coveredBy but does not count.
	Protected    bool       `json:"protected"`
	LastBackupAt *time.Time `json:"lastBackupAt,omitempty"`
}

type backupSuggestion struct {
	Name            string   `json:"name"`
	Sources         []string `json:"sources"`
	Excludes        []string `json:"excludes,omitempty"`
	SQLitePaths     []string `json:"sqlitePaths,omitempty"`
	DatabaseDumps   []int64  `json:"databaseDumps,omitempty"`
	PauseContainers []string `json:"pauseContainers,omitempty"`
}

type backupCoverer struct {
	JobID   int64  `json:"jobId"`
	JobName string `json:"jobName"`
	Enabled bool   `json:"enabled"`
}

type backupResourceReport struct {
	Resources []backupResource `json:"resources"`
	// Unavailable names the owners that could not be asked, so an empty
	// volume list reads as "Docker is down" rather than "nothing to protect".
	Unavailable map[string]string `json:"unavailable"`
}

// backupResources asks each owner what it has and folds the job list over
// the answers. Owners that cannot answer are reported, not treated as empty.
func (s *Server) backupResources(ctx context.Context, jobs []*backups.Job) backupResourceReport {
	report := backupResourceReport{Resources: []backupResource{}, Unavailable: map[string]string{}}
	add := func(res backupResource) {
		if res.Suggest.Name == "" {
			res.Suggest.Name = res.Name
		}
		if res.CoveredBy == nil {
			res.CoveredBy = []backupCoverer{}
		}
		report.Resources = append(report.Resources, res)
	}

	// The dashboard itself: its SQLite file, sealed secrets and settings.
	// The staging directory lives inside it and is what the archive is
	// written to, so the suggestion excludes it; deployment workspaces are
	// build scratch and are excluded for size.
	dataDir := filepath.Clean(s.Cfg.DataDir)
	add(backupResource{
		Kind: "dashboard", ID: "dashboard", Name: "Just Dashboard",
		Detail: "Settings, accounts, saved connections and deployment records",
		Paths:  []string{dataDir},
		Suggest: backupSuggestion{
			Name:        "Dashboard data",
			Sources:     []string{dataDir},
			Excludes:    []string{filepath.Join(dataDir, "staging"), filepath.Join(dataDir, "deployment-workspaces")},
			SQLitePaths: []string{filepath.Join(dataDir, store.DatabaseFile)},
		},
	})

	if s.Cfg.NginxDir != "" {
		if st, err := os.Stat(s.Cfg.NginxDir); err == nil && st.IsDir() {
			dir := filepath.Clean(s.Cfg.NginxDir)
			add(backupResource{Kind: "proxy", ID: "nginx", Name: "Nginx configuration", Detail: dir,
				Paths: []string{dir}, Suggest: backupSuggestion{Sources: []string{dir}}})
		}
	}
	if s.Cfg.CaddyFile != "" {
		if st, err := os.Stat(s.Cfg.CaddyFile); err == nil && !st.IsDir() {
			dir := filepath.Dir(filepath.Clean(s.Cfg.CaddyFile))
			add(backupResource{Kind: "proxy", ID: "caddy", Name: "Caddy configuration", Detail: dir,
				Paths: []string{dir}, Suggest: backupSuggestion{Sources: []string{dir}}})
		}
	}

	if volumes, err := s.modules.docker.ListVolumesWithUsers(ctx); err != nil {
		report.Unavailable["docker"] = dockerUnavailableReason(err)
	} else {
		for _, v := range volumes {
			if v.Driver != "local" || !filepath.IsAbs(v.Mountpoint) {
				continue
			}
			var users, running []string
			for _, u := range v.UsedBy {
				users = append(users, u.Name)
				if u.State == "running" {
					running = append(running, u.Name)
				}
			}
			detail := "Not mounted by any container"
			if len(users) > 0 {
				detail = "Mounted by " + strings.Join(users, ", ")
			}
			add(backupResource{
				Kind: "volume", ID: v.Name, Name: v.Name, Detail: detail,
				Paths: []string{v.Mountpoint},
				Suggest: backupSuggestion{
					Name: "Volume " + v.Name, Sources: []string{v.Mountpoint}, PauseContainers: running,
				},
			})
		}
		if stacks, err := s.modules.docker.ListStacks(ctx, s.Cfg.ComposeRoots); err == nil {
			for _, st := range stacks {
				if !filepath.IsAbs(st.WorkingDir) {
					continue
				}
				var running []string
				for _, svc := range st.Services {
					if svc.State == "running" && svc.Container != "" {
						running = append(running, svc.Container)
					}
				}
				add(backupResource{
					Kind: "stack", ID: st.Name, Name: st.Name, Detail: st.Summary,
					Paths: []string{st.WorkingDir},
					Suggest: backupSuggestion{
						Name: "Stack " + st.Name, Sources: []string{st.WorkingDir}, PauseContainers: running,
					},
				})
			}
		}
	}

	if projects, err := s.modules.deployStore.List(ctx); err == nil {
		for _, p := range projects {
			if p.ArchivedAt != nil || !filepath.IsAbs(p.RepoPath) {
				continue
			}
			add(backupResource{
				Kind: "deployment", ID: p.Name, Name: p.Name, Detail: p.RepoPath,
				Paths:   []string{p.RepoPath},
				Suggest: backupSuggestion{Name: "Deployment " + p.Name, Sources: []string{p.RepoPath}},
			})
		}
	}

	if s.modules.git.Available() {
		if repos, err := s.modules.git.Discover(ctx); err == nil {
			for _, repo := range repos {
				add(backupResource{
					Kind: "repository", ID: repo.Path, Name: repo.Name, Detail: repo.Path,
					Paths:   []string{repo.Path},
					Suggest: backupSuggestion{Name: "Repository " + repo.Name, Sources: []string{repo.Path}},
				})
			}
		}
	} else {
		report.Unavailable["git"] = "git is not installed on this host"
	}

	if rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM db_connections ORDER BY name`); err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			conn, dsn, err := s.dbConnRow(ctx, id)
			if err != nil {
				continue
			}
			res := backupResource{
				Kind: "database", ID: conn.Name, Name: conn.Name,
				Detail:       string(conn.Driver) + " · " + firstNonEmpty(conn.Database, conn.Host),
				ConnectionID: conn.ID,
				Suggest:      backupSuggestion{Name: "Database " + conn.Name, DatabaseDumps: []int64{conn.ID}},
			}
			// A SQLite connection is also a file, and a job that snapshots
			// that file protects it as well as a dump would.
			if conn.Driver == dbx.DriverSQLite {
				if info, err := dbx.ParseDSN(conn.Driver, dsn); err == nil && filepath.IsAbs(info.Database) {
					res.Paths = []string{info.Database}
				}
			}
			add(res)
		}
	}

	for i := range report.Resources {
		coverResource(&report.Resources[i], jobs)
	}
	// Unprotected first, then by kind and name, so what needs doing is at
	// the top of the list.
	sort.SliceStable(report.Resources, func(i, j int) bool {
		a, b := report.Resources[i], report.Resources[j]
		if a.Protected != b.Protected {
			return !a.Protected
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return report
}

// coverResource decides which jobs protect a resource. A file resource is
// covered when every one of its paths sits under a job source; a database
// when a job dumps it natively, or, for SQLite, snapshots or archives its
// file.
func coverResource(res *backupResource, jobs []*backups.Job) {
	for _, job := range jobs {
		covers := false
		if res.ConnectionID != 0 {
			for _, id := range job.DatabaseDumps {
				if id == res.ConnectionID {
					covers = true
				}
			}
			if !covers && len(res.Paths) > 0 {
				for _, p := range job.SQLitePaths {
					if filepath.Clean(p) == filepath.Clean(res.Paths[0]) {
						covers = true
					}
				}
			}
		}
		if !covers && len(res.Paths) > 0 {
			covers = true
			for _, p := range res.Paths {
				if !anySourceCovers(job.Sources, p) {
					covers = false
					break
				}
			}
		}
		if !covers {
			continue
		}
		res.CoveredBy = append(res.CoveredBy, backupCoverer{JobID: job.ID, JobName: job.Name, Enabled: job.Enabled})
		if job.Enabled {
			res.Protected = true
		}
		if job.LastSuccessAt != nil && (res.LastBackupAt == nil || job.LastSuccessAt.After(*res.LastBackupAt)) {
			at := *job.LastSuccessAt
			res.LastBackupAt = &at
		}
	}
}

func anySourceCovers(sources []string, wanted string) bool {
	wanted = filepath.Clean(wanted)
	for _, source := range sources {
		relative, err := filepath.Rel(filepath.Clean(source), wanted)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func dockerUnavailableReason(err error) string {
	if errors.Is(err, dockerx.ErrUnavailable) {
		return "The Docker daemon is not reachable from this dashboard."
	}
	return err.Error()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
