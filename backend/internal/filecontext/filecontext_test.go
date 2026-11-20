package filecontext

import (
	"testing"
)

func allRead() map[string]bool {
	return map[string]bool{
		SourceDocker: true, SourceProxy: true, SourceBackups: true, SourceGit: true,
		SourceDeploy: true, SourceDatabases: true, SourceProcesses: true, SourceDashboard: true,
	}
}

func kinds(facts []Fact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.Kind)
	}
	return out
}

func find(facts []Fact, kind string) *Fact {
	for i := range facts {
		if facts[i].Kind == kind {
			return &facts[i]
		}
	}
	return nil
}

// One synthetic thing per source, each where it would be on a real server.
func syntheticThings() []Thing {
	return []Thing{
		{Fact: Fact{Kind: KindVolume, Source: SourceDocker, Names: []string{"shop_pgdata", "shop-db"}, Image: "postgres:16", Href: "/docker/volumes/shop_pgdata", Inherits: true},
			Paths: []string{"/var/lib/docker/volumes/shop_pgdata/_data", "/var/lib/docker/volumes/shop_pgdata"}, Notable: true},
		{Fact: Fact{Kind: KindBind, Source: SourceDocker, Names: []string{"api", "/app/uploads"}, Image: "node:22", Href: "/docker/containers/abc", Inherits: true},
			Paths: []string{"/srv/shop/uploads"}, Notable: true},
		{Fact: Fact{Kind: KindCompose, Source: SourceDocker, Names: []string{"shop"}, Product: "docker-compose", Href: "/docker/stacks/shop", Inherits: true},
			Paths: []string{"/srv/shop"}, Notable: true},
		{Fact: Fact{Kind: KindComposeFile, Source: SourceDocker, Names: []string{"shop"}, Product: "docker-compose", Href: "/docker/stacks/shop"},
			Paths: []string{"/srv/shop/compose.yaml"}},
		{Fact: Fact{Kind: KindSiteRoot, Source: SourceProxy, Names: []string{"example.com"}, Product: "nginx", Href: "/proxy/sites/example.com", Inherits: true},
			Paths: []string{"/var/www/example"}, Notable: true},
		{Fact: Fact{Kind: KindSiteConfig, Source: SourceProxy, Names: []string{"example.com"}, Product: "nginx", Href: "/proxy/sites/example.com"},
			Paths: []string{"/etc/nginx/sites-available/example.com"}},
		{Fact: Fact{Kind: KindGit, Source: SourceGit, Names: []string{"app"}, Product: "git", Detail: "main", Href: "/git?repo=%2Fsrv%2Fapp", Inherits: true},
			Paths: []string{"/srv/app"}, Notable: true},
		{Fact: Fact{Kind: KindGit, Source: SourceGit, Names: []string{"app2"}, Product: "git", Detail: "main", Href: "/git?repo=%2Fsrv%2Fapp2", Inherits: true},
			Paths: []string{"/srv/app2"}, Notable: true},
		{Fact: Fact{Kind: KindDeploySource, Source: SourceDeploy, Names: []string{"shop"}, Href: "/deploy/3", Inherits: true},
			Paths: []string{"/srv/app"}, Notable: true},
		{Fact: Fact{Kind: KindDatabaseData, Source: SourceDatabases, Names: []string{"main"}, Product: "postgresql", Detail: "16", Href: "/databases", Inherits: true},
			Paths: []string{"/var/lib/postgresql/16/main"}, Notable: true},
		{Fact: Fact{Kind: KindUnitFile, Source: SourceProcesses, Names: []string{"nginx.service"}, Product: "systemd", Href: "/processes/services/nginx.service"},
			Paths: []string{"/lib/systemd/system/nginx.service"}},
		{Fact: Fact{Kind: KindDashboardData, Source: SourceDashboard, Names: []string{}, Href: "/dashboard", Inherits: true},
			Paths: []string{"/var/lib/just-dashboard"}},
	}
}

func syntheticJobs() []Job {
	return []Job{{
		Fact:    Fact{Kind: KindBackup, Source: SourceBackups, Names: []string{"nightly srv"}, Target: "b2", Schedule: "0 3 * * *", State: BackupOK, Href: "/backups/1"},
		Sources: []string{"/srv"}, Excludes: []string{"/srv/shop/cache"}, Enabled: true,
	}}
}

func TestEachSourceSaysWhatAFolderIs(t *testing.T) {
	cases := map[string]struct {
		folder string
		kind   string
	}{
		"a Docker volume, by its _data":      {"/var/lib/docker/volumes/shop_pgdata/_data", KindVolume},
		"a Docker volume, by its own folder": {"/var/lib/docker/volumes/shop_pgdata", KindVolume},
		"a bind mount":                       {"/srv/shop/uploads", KindBind},
		"a Compose project":                  {"/srv/shop", KindCompose},
		"a site's root":                      {"/var/www/example", KindSiteRoot},
		"a Git checkout":                     {"/srv/app", KindGit},
		"a deployment's source":              {"/srv/app", KindDeploySource},
		"a database's data directory":        {"/var/lib/postgresql/16/main", KindDatabaseData},
		"the dashboard's own data":           {"/var/lib/just-dashboard", KindDashboardData},
	}
	for name, c := range cases {
		got := Resolve(Input{Folder: c.folder, Things: syntheticThings(), Jobs: syntheticJobs(), Read: allRead()})
		f := find(got.Facts, c.kind)
		if f == nil {
			t.Errorf("%s: no %s fact in %v", name, c.kind, kinds(got.Facts))
			continue
		}
		if f.From != "" {
			t.Errorf("%s: own fact marked inherited from %q", name, f.From)
		}
	}
}

func TestFilesAreMatchedExactly(t *testing.T) {
	things := syntheticThings()
	got := Resolve(Input{
		Folder: "/srv/shop",
		Children: []Child{
			{Path: "/srv/shop/compose.yaml"},
			{Path: "/srv/shop/uploads"},
			{Path: "/srv/shop/README.md"},
		},
		Things: things, Read: allRead(),
	})
	if f := find(got.Children["/srv/shop/compose.yaml"], KindComposeFile); f == nil {
		t.Fatalf("compose file not named: %v", got.Children)
	}
	if f := find(got.Children["/srv/shop/uploads"], KindBind); f == nil {
		t.Fatalf("bind mount child not named: %v", got.Children)
	}
	if facts, ok := got.Children["/srv/shop/README.md"]; ok {
		t.Fatalf("a plain file got facts: %v", facts)
	}
	// A file is not a folder: nothing inside a unit file inherits from it.
	unit := Resolve(Input{Folder: "/lib/systemd/system/nginx.service/x", Things: things, Read: allRead()})
	if find(unit.Facts, KindUnitFile) != nil {
		t.Fatalf("a unit file's fact was inherited: %v", unit.Facts)
	}
}

func TestInheritedFactsNameTheFolderTheyComeFrom(t *testing.T) {
	got := Resolve(Input{Folder: "/var/lib/docker/volumes/shop_pgdata/_data/base/16384", Things: syntheticThings(), Read: allRead()})
	f := find(got.Facts, KindVolume)
	if f == nil {
		t.Fatalf("volume not inherited: %v", got.Facts)
	}
	if f.From != "/var/lib/docker/volumes/shop_pgdata/_data" {
		t.Fatalf("inherited from %q, want the nearest folder the volume is", f.From)
	}
	n := 0
	for _, f := range got.Facts {
		if f.Kind == KindVolume {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the volume's two folders gave %d facts, want the nearest one: %v", n, got.Facts)
	}
}

func TestMatchesAreOnWholeSegments(t *testing.T) {
	got := Resolve(Input{Folder: "/srv/app2", Things: syntheticThings(), Read: allRead()})
	for _, f := range got.Facts {
		if f.Kind == KindDeploySource || (f.Kind == KindGit && f.Names[0] == "app") {
			t.Fatalf("/srv/app2 read as inside /srv/app: %+v", f)
		}
	}
	if !Within("/srv/app", "/srv/app/x") || Within("/srv/app", "/srv/app2") || !Within("/", "/etc") {
		t.Fatal("Within is not segment-wise")
	}
}

func TestASymlinkedChildIsMatchedAsWhatItPointsAt(t *testing.T) {
	got := Resolve(Input{
		Folder:   "/home/ops",
		Children: []Child{{Path: "/home/ops/pgdata", Real: "/var/lib/docker/volumes/shop_pgdata/_data"}},
		Things:   syntheticThings(), Read: allRead(),
	})
	if find(got.Children["/home/ops/pgdata"], KindVolume) == nil {
		t.Fatalf("link to a volume not named: %v", got.Children)
	}
}

func TestBackupCoverage(t *testing.T) {
	things, jobs := syntheticThings(), syntheticJobs()
	srv := Resolve(Input{
		Folder: "/srv/shop",
		Children: []Child{
			{Path: "/srv/shop/cache"},
			{Path: "/srv/shop/uploads"},
		},
		Things: things, Jobs: jobs, Read: allRead(),
	})
	b := find(srv.Facts, KindBackup)
	if b == nil || b.From != "/srv" || b.Names[0] != "nightly srv" {
		t.Fatalf("folder's coverage = %+v, want the nightly job from /srv", b)
	}
	if find(srv.Facts, KindNotBackedUp) != nil {
		t.Fatal("a covered folder said not backed up")
	}
	if find(srv.Children["/srv/shop/cache"], KindBackupExcluded) == nil {
		t.Fatalf("an excluded child did not say so: %v", srv.Children)
	}
	if find(srv.Children["/srv/shop/uploads"], KindBackup) != nil {
		t.Fatal("a child repeated the folder's coverage")
	}

	// A folder that matters and that no job covers.
	vol := Resolve(Input{Folder: "/var/lib/docker/volumes/shop_pgdata/_data", Things: things, Jobs: jobs, Read: allRead()})
	if find(vol.Facts, KindNotBackedUp) == nil {
		t.Fatalf("an uncovered volume did not say so: %v", kinds(vol.Facts))
	}
	// A folder that does not matter is not nagged about.
	etc := Resolve(Input{Folder: "/etc", Things: things, Jobs: jobs, Read: allRead()})
	if find(etc.Facts, KindNotBackedUp) != nil {
		t.Fatal("an ordinary folder was said to be not backed up")
	}
	// A paused job does not protect.
	paused := []Job{jobs[0]}
	paused[0].Enabled = false
	app := Resolve(Input{Folder: "/srv/app", Things: things, Jobs: paused, Read: allRead()})
	if f := find(app.Facts, KindBackup); f == nil || f.State != BackupPaused {
		t.Fatalf("paused coverage = %+v", f)
	}
	if find(app.Facts, KindNotBackedUp) == nil {
		t.Fatal("a folder covered only by a paused job was not said to be unprotected")
	}
	// A notable child of an uncovered folder says it on its own.
	vols := Resolve(Input{
		Folder:   "/var/lib/docker/volumes",
		Children: []Child{{Path: "/var/lib/docker/volumes/shop_pgdata"}},
		Things:   things, Jobs: jobs, Read: allRead(),
	})
	if find(vols.Children["/var/lib/docker/volumes/shop_pgdata"], KindNotBackedUp) == nil {
		t.Fatalf("an uncovered volume in the list did not say so: %v", vols.Children)
	}
}

func TestASourceNotReadIsLeftOut(t *testing.T) {
	read := allRead()
	delete(read, SourceGit)
	delete(read, SourceBackups)
	got := Resolve(Input{Folder: "/srv/app", Things: syntheticThings(), Jobs: syntheticJobs(), Read: read})
	if find(got.Facts, KindGit) != nil {
		t.Fatal("a source not read in time still answered")
	}
	if find(got.Facts, KindDeploySource) == nil {
		t.Fatal("the other sources were dropped with it")
	}
	// Without the backups, nothing can be said about coverage either way.
	if find(got.Facts, KindNotBackedUp) != nil || find(got.Facts, KindBackup) != nil {
		t.Fatalf("coverage said without the backups: %v", kinds(got.Facts))
	}
}

func TestOnlySourcesTheReaderCanOpenAreShown(t *testing.T) {
	got := Resolve(Input{
		Folder: "/srv/app", Things: syntheticThings(), Jobs: syntheticJobs(), Read: allRead(),
		Can: func(source string) bool { return source != SourceDeploy && source != SourceBackups },
	})
	if find(got.Facts, KindDeploySource) != nil {
		t.Fatal("a source the reader cannot open was shown")
	}
	if find(got.Facts, KindBackup) != nil || find(got.Facts, KindNotBackedUp) != nil {
		t.Fatal("backup coverage shown to a reader who cannot open Backups")
	}
	if find(got.Facts, KindGit) == nil {
		t.Fatal("an open source was filtered too")
	}
}

func TestRelativeAndEmptyPathsMatchNothing(t *testing.T) {
	things := []Thing{{Fact: Fact{Kind: KindPM2, Source: SourceProcesses, Inherits: true}, Paths: []string{"", "relative/dir"}}}
	got := Resolve(Input{Folder: "/", Children: []Child{{Path: "/relative"}}, Things: things, Read: allRead()})
	if len(got.Facts) != 0 || len(got.Children) != 0 {
		t.Fatalf("a relative path matched: %+v", got)
	}
}
