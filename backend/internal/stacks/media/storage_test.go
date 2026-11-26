package media

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// fakeVolume is the shared media volume as the services' commands see it:
// folders with an owner, and which services mount it read-only.
type fakeVolume struct {
	owners   map[string]string
	readOnly map[string]bool
	// refuse makes writing in a folder fail for everyone but root.
	refuse map[string]bool
	execs  []string
}

func newFakeVolume() *fakeVolume {
	return &fakeVolume{owners: map[string]string{DataRoot: "0:0"}, readOnly: map[string]bool{}, refuse: map[string]bool{}}
}

func (v *fakeVolume) exec(service, user string, argv []string) (stacks.ExecResult, error) {
	v.execs = append(v.execs, service+" "+user+" "+strings.Join(argv, " "))
	arguments := argv[1:]
	if at := slices.Index(argv, "--"); at >= 0 {
		arguments = argv[at+1:]
	}
	var out, complaint strings.Builder
	code := 0
	fail := func(format string, args ...any) {
		fmt.Fprintf(&complaint, format+"\n", args...)
		code = 1
	}
	switch argv[0] {
	case "stat":
		for _, path := range arguments {
			if owner, found := v.owners[path]; found {
				fmt.Fprintf(&out, "%s %s\n", owner, path)
			} else {
				fail("stat: cannot statx '%s': No such file or directory", path)
			}
		}
	case "mkdir":
		for _, path := range arguments {
			if v.readOnly[service] {
				fail("mkdir: cannot create directory '%s': Read-only file system", path)
				continue
			}
			for parent := path; parent != DataRoot && parent != "/"; parent = parent[:strings.LastIndex(parent, "/")] {
				if _, found := v.owners[parent]; !found {
					v.owners[parent] = user
				}
			}
		}
	case "chown":
		if slices.Contains(argv, "-R") {
			fail("chown: this volume must never be changed recursively")
		}
		for _, path := range arguments {
			v.owners[path] = argv[1]
		}
	case "touch":
		for _, path := range arguments {
			folder := path[:strings.LastIndex(path, "/")]
			switch {
			case v.readOnly[service]:
				fail("touch: cannot touch '%s': Read-only file system", path)
			case v.refuse[folder] || (user != "" && user != "0:0" && v.owners[folder] != user):
				fail("touch: cannot touch '%s': Permission denied", path)
			}
		}
	case "rm":
	case "test":
		if _, found := v.owners[argv[2]]; !found {
			code = 1
		}
	default:
		return stacks.ExecResult{ExitCode: -1}, fmt.Errorf("the volume has no %s", argv[0])
	}
	return stacks.ExecResult{Stdout: out.String(), Stderr: complaint.String(), ExitCode: code}, nil
}

func (v *fakeVolume) take() []string {
	taken := v.execs
	v.execs = nil
	return taken
}

func completeServices() map[string]string {
	return map[string]string{"jellyfin": "", "seerr": "", "radarr": "", "sonarr": "", "prowlarr": "", "qbittorrent": "", "bazarr": "", "recyclarr": ""}
}

func storageEnv(preset string, services map[string]string, volume *fakeVolume) *stackstest.Fake {
	env := newFake(preset, services)
	env.ExecFunc = volume.exec
	return env
}

func TestStorageFoldersMakesThemOnceAndProvesEveryWriter(t *testing.T) {
	volume := newFakeVolume()
	volume.readOnly["jellyfin"] = true
	env := storageEnv("complete", completeServices(), volume)

	result := mustRunStep(t, env, "storage.folders")
	for _, folder := range []string{MoviesDownload, TVDownload, MoviesLibrary, TVLibrary, DownloadsRoot, MediaRoot} {
		if volume.owners[folder] != "1000:1000" {
			t.Errorf("%s belongs to %q", folder, volume.owners[folder])
		}
	}
	if volume.owners[DataRoot] != "0:0" {
		t.Errorf("the volume's own root was given away: %s", volume.owners[DataRoot])
	}
	first := volume.take()
	for _, expected := range []string{
		"qbittorrent 0:0 mkdir -p -- /data/downloads/movies /data/downloads/tv /data/media/movies /data/media/tv",
		"qbittorrent 1000:1000 touch -- /data/downloads/movies/.just-dashboard-write-test /data/downloads/tv/.just-dashboard-write-test",
		"qbittorrent 1000:1000 rm -f -- /data/downloads/movies/.just-dashboard-write-test /data/downloads/tv/.just-dashboard-write-test",
		"radarr 1000:1000 touch -- /data/downloads/movies/.just-dashboard-write-test /data/media/movies/.just-dashboard-write-test",
		"sonarr 1000:1000 touch -- /data/downloads/tv/.just-dashboard-write-test /data/media/tv/.just-dashboard-write-test",
		"bazarr 1000:1000 touch -- /data/media/movies/.just-dashboard-write-test /data/media/tv/.just-dashboard-write-test",
		"jellyfin  test -r /data/media/movies -a -x /data/media/movies",
		"jellyfin  test -r /data/media/tv -a -x /data/media/tv",
	} {
		if !slices.Contains(first, expected) {
			t.Errorf("the first run did not run %q\n%s", expected, strings.Join(first, "\n"))
		}
	}
	if !strings.Contains(result.Message, "Made /data/downloads/movies") || !strings.Contains(result.Message, "Jellyfin can read the library") {
		t.Errorf("message = %q", result.Message)
	}

	// The second run looks, proves the writes again and changes nothing.
	result = mustRunStep(t, env, "storage.folders")
	for _, command := range volume.take() {
		if strings.Contains(command, " mkdir ") || strings.Contains(command, " chown ") {
			t.Errorf("the second run ran %q", command)
		}
		if strings.HasPrefix(command, "jellyfin ") && strings.Contains(command, "touch") {
			t.Errorf("Jellyfin was asked to write to a volume it mounts read-only: %q", command)
		}
	}
	if !strings.HasPrefix(result.Message, "The download and library folders are in place") {
		t.Errorf("message = %q", result.Message)
	}
}

func TestStorageFoldersLeavesWhatIsAlreadyThereAlone(t *testing.T) {
	volume := newFakeVolume()
	// A library somebody copied in: its folder and a parent under an account
	// of their own, with content the step must never look at.
	volume.owners[MediaRoot] = "1001:1001"
	volume.owners[MoviesLibrary] = "1001:1001"
	volume.owners[MoviesLibrary+"/Some Film (2020)"] = "1001:1001"
	env := storageEnv("complete", completeServices(), volume)

	mustRunStep(t, env, "storage.folders")
	if volume.owners[MediaRoot] != "1001:1001" {
		t.Errorf("a parent folder somebody owns was taken: %s", volume.owners[MediaRoot])
	}
	if volume.owners[MoviesLibrary+"/Some Film (2020)"] != "1001:1001" {
		t.Errorf("existing content changed owner: %s", volume.owners[MoviesLibrary+"/Some Film (2020)"])
	}
	if volume.owners[MoviesLibrary] != "1000:1000" || volume.owners[DownloadsRoot] != "1000:1000" {
		t.Errorf("the stack's own folders were not given to the runtime account: %v", volume.owners)
	}
	for _, command := range volume.take() {
		if strings.Contains(command, "Some Film") || strings.Contains(command, " -R") {
			t.Errorf("a command reached into existing content: %q", command)
		}
	}
}

func TestStorageFoldersInEssentialUsesJellyfinAndItsOwnAccount(t *testing.T) {
	volume := newFakeVolume()
	env := storageEnv("essential", map[string]string{"jellyfin": "", "seerr": ""}, volume)

	result := mustRunStep(t, env, "storage.folders")
	commands := volume.take()
	if !slices.Contains(commands, "jellyfin 0:0 mkdir -p -- /data/downloads/movies /data/downloads/tv /data/media/movies /data/media/tv") {
		t.Errorf("the folders were not made in Jellyfin:\n%s", strings.Join(commands, "\n"))
	}
	if !slices.Contains(commands, "jellyfin  touch -- /data/media/movies/.just-dashboard-write-test /data/media/tv/.just-dashboard-write-test") {
		t.Errorf("Jellyfin did not prove it can write as its own account:\n%s", strings.Join(commands, "\n"))
	}
	if !strings.Contains(result.Message, "jellyfin can write to them") {
		t.Errorf("message = %q", result.Message)
	}
}

func TestStorageFoldersNamesTheFolderAndTheAccountThatCannotWrite(t *testing.T) {
	volume := newFakeVolume()
	volume.readOnly["jellyfin"] = true
	volume.refuse[MoviesLibrary] = true
	env := storageEnv("complete", completeServices(), volume)

	_, err := runStep(t, env, "storage.folders")
	if err == nil || !stacks.IsPermanent(err) {
		t.Fatalf("expected a permanent refusal, got %v", err)
	}
	for _, expected := range []string{"radarr cannot write to /data/media/movies as 1000:1000", "Permission denied"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not say %q", err, expected)
		}
	}
	if commands := volume.take(); !slices.ContainsFunc(commands, func(command string) bool { return strings.HasPrefix(command, "radarr 1000:1000 rm -f") }) {
		t.Errorf("the write test was not cleaned up after the refusal")
	}
}

func TestStorageFoldersRetriesWhenTheContainerDoesNotAnswer(t *testing.T) {
	env := storageEnv("complete", completeServices(), newFakeVolume())
	env.ExecFunc = func(string, string, []string) (stacks.ExecResult, error) {
		return stacks.ExecResult{ExitCode: -1}, fmt.Errorf("container is restarting")
	}
	_, err := runStep(t, env, "storage.folders")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "qbittorrent") {
		t.Fatalf("expected an error the runner retries, naming the service; got %v", err)
	}
}
