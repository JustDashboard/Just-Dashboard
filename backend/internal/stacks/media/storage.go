package media

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// probeName is the file each writer creates and removes to prove it can
// write. A fixed name: one left behind by an interrupted run is removed by
// the next.
const probeName = ".just-dashboard-write-test"

// runtimeUser is the account the linuxserver.io images run as, the way an
// exec names it.
var runtimeUser = fmt.Sprintf("%d:%d", RuntimeUID, RuntimeGID)

// libraryFolders and downloadFolders are the folders the stack makes. Their
// parents come with them.
var (
	downloadFolders = []string{MoviesDownload, TVDownload}
	libraryFolders  = []string{MoviesLibrary, TVLibrary}
	parentFolders   = []string{DownloadsRoot, MediaRoot}
)

// storageWriters is which folders each service writes to, and so which it is
// asked to prove it can.
var storageWriters = []struct {
	service string
	folders []string
}{
	{"qbittorrent", []string{MoviesDownload, TVDownload}},
	{"radarr", []string{MoviesDownload, MoviesLibrary}},
	{"sonarr", []string{TVDownload, TVLibrary}},
	{"bazarr", []string{MoviesLibrary, TVLibrary}},
}

// storageFolders makes the download and library folders on the shared media
// volume and proves that every service that must write to them can, as the
// account it really runs as.
//
// Ownership is changed on the folders themselves only, never on what is in
// them: a library somebody copied in keeps the owner they gave it.
func storageFolders(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	builder := ""
	for _, candidate := range []string{"qbittorrent", "jellyfin"} {
		// Complete mounts the volume read-only in Jellyfin, so the folders are
		// made where it is writable: qBittorrent first.
		if env.Has(candidate) {
			builder = candidate
			break
		}
	}
	if builder == "" {
		return stacks.Result{}, stacks.Permanent(fmt.Errorf("no service of this stack mounts the media volume"))
	}

	folders := slices.Concat(downloadFolders, libraryFolders)
	before, err := folderOwners(ctx, env, builder, slices.Concat(parentFolders, folders))
	if err != nil {
		return stacks.Result{}, err
	}
	var missing []string
	for _, folder := range folders {
		if _, found := before[folder]; !found {
			missing = append(missing, folder)
		}
	}
	if len(missing) > 0 {
		if err := execOrFail(ctx, env, builder, "0:0", append([]string{"mkdir", "-p", "--"}, missing...), "make "+strings.Join(missing, ", ")); err != nil {
			return stacks.Result{}, err
		}
		env.Logf("made %s", strings.Join(missing, ", "))
	}

	after, err := folderOwners(ctx, env, builder, slices.Concat(parentFolders, folders))
	if err != nil {
		return stacks.Result{}, err
	}
	var give []string
	for _, folder := range folders {
		owner, found := after[folder]
		if !found {
			return stacks.Result{}, fmt.Errorf("%s is still missing after it was made", folder)
		}
		if owner != runtimeUser {
			give = append(give, folder)
		}
	}
	for _, parent := range parentFolders {
		// A parent mkdir just made is root's, and qBittorrent saves a download
		// with no category straight into /data/downloads. One somebody gave to
		// another account is theirs to keep.
		if after[parent] == "0:0" {
			give = append(give, parent)
		}
	}
	if len(give) > 0 {
		if err := execOrFail(ctx, env, builder, "0:0", append([]string{"chown", runtimeUser, "--"}, give...), "give "+strings.Join(give, ", ")+" to "+runtimeUser); err != nil {
			return stacks.Result{}, err
		}
		env.Logf("gave %s to %s", strings.Join(give, ", "), runtimeUser)
		final, err := folderOwners(ctx, env, builder, folders)
		if err != nil {
			return stacks.Result{}, err
		}
		for _, folder := range folders {
			if final[folder] != runtimeUser {
				return stacks.Result{}, fmt.Errorf("%s belongs to %s after it was given to %s", folder, final[folder], runtimeUser)
			}
		}
	}

	var proved []string
	for _, writer := range storageWriters {
		if !env.Has(writer.service) {
			continue
		}
		if err := proveWrite(ctx, env, writer.service, runtimeUser, runtimeUser, writer.folders); err != nil {
			return stacks.Result{}, err
		}
		proved = append(proved, writer.service)
	}
	if env.Has("jellyfin") {
		if env.Preset() == "complete" {
			if err := proveRead(ctx, env, "jellyfin", libraryFolders); err != nil {
				return stacks.Result{}, err
			}
		} else {
			// Without the managers Jellyfin is the only service on the volume,
			// and it writes there itself: artwork and subtitles saved beside
			// the media, and deleting a title from its own interface.
			if err := proveWrite(ctx, env, "jellyfin", "", "its own account", libraryFolders); err != nil {
				return stacks.Result{}, err
			}
			proved = append(proved, "jellyfin")
		}
	}

	message := "The download and library folders are in place"
	if len(missing) > 0 {
		message = "Made " + strings.Join(missing, ", ")
	}
	if len(proved) > 0 {
		message += "; " + strings.Join(proved, ", ") + " can write to them"
	}
	if env.Has("jellyfin") && env.Preset() == "complete" {
		message += "; Jellyfin can read the library"
	}
	return stacks.Result{Message: message}, nil
}

// folderOwners is "uid:gid" for each of paths that exists, read in service as
// root. A path that does not exist is absent from the result.
func folderOwners(ctx context.Context, env stacks.Env, service string, paths []string) (map[string]string, error) {
	result, err := env.Exec(ctx, service, "0:0", append([]string{"stat", "-c", "%u:%g %n", "--"}, paths...))
	if err != nil {
		return nil, fmt.Errorf("could not look at the media folders in %s: %w", service, err)
	}
	owners := map[string]string{}
	for line := range strings.Lines(result.Stdout) {
		owner, name, found := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		if found && slices.Contains(paths, name) {
			owners[name] = owner
		}
	}
	// stat exits 1 when any path is missing, which is an answer here, not a
	// failure. One that printed nothing and did not say "No such file" is.
	if result.ExitCode != 0 && len(owners) == 0 && !strings.Contains(result.Stderr, "No such file") {
		return nil, fmt.Errorf("could not look at the media folders in %s: %s", service, oneLine(result.Stderr))
	}
	return owners, nil
}

func execOrFail(ctx context.Context, env stacks.Env, service, user string, argv []string, what string) error {
	result, err := env.Exec(ctx, service, user, argv)
	if err != nil {
		return fmt.Errorf("could not %s in %s: %w", what, service, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("could not %s in %s: %s", what, service, oneLine(result.Stderr))
	}
	return nil
}

// proveWrite creates and removes a file in each folder as user. A refusal is
// permanent: the folders were just given to the runtime account, so another
// attempt meets the same answer.
func proveWrite(ctx context.Context, env stacks.Env, service, user, userName string, folders []string) error {
	probes := make([]string, len(folders))
	for index, folder := range folders {
		probes[index] = folder + "/" + probeName
	}
	created, err := env.Exec(ctx, service, user, append([]string{"touch", "--"}, probes...))
	if err != nil {
		return fmt.Errorf("could not test writing in %s: %w", service, err)
	}
	// Whatever was created is removed whether or not every folder took one.
	removed, removeErr := env.Exec(ctx, service, user, append([]string{"rm", "-f", "--"}, probes...))
	if created.ExitCode != 0 {
		refused := folders[0]
		for _, folder := range folders {
			if strings.Contains(created.Stderr, folder+"/"+probeName) {
				refused = folder
				break
			}
		}
		return stacks.Permanent(fmt.Errorf("%s cannot write to %s as %s: %s", service, refused, userName, oneLine(created.Stderr)))
	}
	if removeErr != nil {
		return fmt.Errorf("could not remove the write test from %s: %w", service, removeErr)
	}
	if removed.ExitCode != 0 {
		return stacks.Permanent(fmt.Errorf("%s created a file in %s as %s but cannot remove it: %s", service, strings.Join(folders, " and "), userName, oneLine(removed.Stderr)))
	}
	return nil
}

// proveRead checks a service that mounts the volume read-only can list each
// folder as the account it runs as.
func proveRead(ctx context.Context, env stacks.Env, service string, folders []string) error {
	for _, folder := range folders {
		result, err := env.Exec(ctx, service, "", []string{"test", "-r", folder, "-a", "-x", folder})
		if err != nil {
			return fmt.Errorf("could not test reading in %s: %w", service, err)
		}
		if result.ExitCode != 0 {
			return stacks.Permanent(fmt.Errorf("%s cannot read %s as its own account", service, folder))
		}
	}
	return nil
}

// oneLine is a command's complaint on one line, bounded.
func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "the command failed without saying why"
	}
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}
