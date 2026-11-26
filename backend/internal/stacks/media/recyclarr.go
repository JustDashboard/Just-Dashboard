package media

// Recyclarr is a command-line tool, not a service with an API: it reads a
// YAML file, fetches the TRaSH Guides, and brings Radarr's and Sonarr's
// custom formats, quality sizes and quality profiles in line with them. Its
// image runs it once a day by itself; this step writes the file it reads
// and runs it once now, so the profiles exist before Seerr is told to use
// them.
//
// The file's format is Recyclarr 8's (https://recyclarr.dev/reference/ and
// the schema at https://schemas.recyclarr.dev/v8/config-schema.json); the
// profile ids are the trash_id values of the guide's own quality profiles,
// as the official config templates of the pinned image name them
// (`recyclarr config list templates`: hd-bluray-web, uhd-bluray-web,
// web-1080p, web-2160p).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

const (
	recyclarrConfigPath = "/config/recyclarr.yml"
	// The instance names in the file, which Recyclarr prefixes its report
	// lines with.
	recyclarrMovies = "just-dashboard-movies"
	recyclarrSeries = "just-dashboard-series"
	// The profile both applications ship with, used for 720p.
	recyclarrBuiltIn720p = "HD-720p"
)

// recyclarrChoice is what one quality option means in each application: a
// guide profile Recyclarr creates and keeps, or none.
type recyclarrChoice struct {
	radarrTrashID, radarrGuideName string
	sonarrTrashID, sonarrGuideName string
}

// recyclarrChoices maps the stack's quality option to the guide's profiles.
// 1080p and 2160p are the profiles the guide's own starter templates use
// for films ("HD Bluray + WEB", "UHD Bluray + WEB") and series ("WEB-1080p",
// "WEB-2160p"). The guide has no 720p profile at all, so 720p names none:
// the applications' built-in "HD-720p" profile is used as it is, and
// Recyclarr keeps only the quality sizes.
var recyclarrChoices = map[string]recyclarrChoice{
	"720p": {},
	"1080p": {
		radarrTrashID: "d1d67249d3890e49bc12e275d989a7e9", radarrGuideName: "HD Bluray + WEB",
		sonarrTrashID: "72dae194fc92bf828f32cde7744e51a1", sonarrGuideName: "WEB-1080p",
	},
	"2160p": {
		radarrTrashID: "64fb5f9858489bdac2af690e27c8f42f", radarrGuideName: "UHD Bluray + WEB",
		sonarrTrashID: "d1498e7d189fbe6c7110ceaabb7473e6", sonarrGuideName: "WEB-2160p",
	},
}

// recyclarrProfileName is the name a guide profile is given in the
// application. The stack's mark keeps it apart from any profile the person
// made: Recyclarr takes over a profile that already has the name it is
// about to use, and a person's own profile never has this one.
func recyclarrProfileName(guideName string) string {
	if guideName == "" {
		return recyclarrBuiltIn720p
	}
	return guideName + arrOwnedSuffix
}

// recyclarrConfig renders the file. The API keys are not in it: Recyclarr
// reads them from the environment the template gives its container
// (!env_var), so the file can be read by anyone who can read the volume.
//
// delete_old_custom_formats is off (its default, written out so a later
// default cannot change it): a custom format the guide drops stays in the
// application rather than being deleted. reset_unmatched_scores is off, so
// a score the person gave one of their own custom formats in these profiles
// is left alone. Recyclarr has no command in a sync that deletes a quality
// profile.
func recyclarrConfig(choice recyclarrChoice) []byte {
	var out strings.Builder
	out.WriteString("# yaml-language-server: $schema=https://schemas.recyclarr.dev/v8/config-schema.json\n")
	out.WriteString("# Written by Just Dashboard for the media stack, and written again whenever the\n")
	out.WriteString("# stack is configured: a change made here does not last. The quality is the\n")
	out.WriteString("# stack's own setting in the dashboard.\n")
	instance := func(kind, name, service string, port int, variable, definition, trashID, guideName string) {
		fmt.Fprintf(&out, "%s:\n  %s:\n", kind, name)
		fmt.Fprintf(&out, "    base_url: http://%s:%d\n", service, port)
		fmt.Fprintf(&out, "    api_key: !env_var %s\n", variable)
		out.WriteString("    delete_old_custom_formats: false\n")
		fmt.Fprintf(&out, "    quality_definition:\n      type: %s\n", definition)
		if trashID == "" {
			return
		}
		out.WriteString("    quality_profiles:\n")
		fmt.Fprintf(&out, "      - trash_id: %s # %s\n", trashID, guideName)
		fmt.Fprintf(&out, "        name: %s\n", recyclarrProfileName(guideName))
		out.WriteString("        reset_unmatched_scores:\n          enabled: false\n")
	}
	instance("radarr", recyclarrMovies, "radarr", PortRadarr, VariableRadarrAPIKey, "movie", choice.radarrTrashID, choice.radarrGuideName)
	instance("sonarr", recyclarrSeries, "sonarr", PortSonarr, VariableSonarrAPIKey, "series", choice.sonarrTrashID, choice.sonarrGuideName)
	return []byte(out.String())
}

// recyclarrPollInterval is how often a sync that is already running is
// looked for again. A variable so tests need not wait.
var recyclarrPollInterval = 3 * time.Second

// recyclarrSync writes Recyclarr's file for the stack's quality, runs a
// sync, and reports the quality profile each application ends up with.
func recyclarrSync(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	quality := strings.TrimSpace(env.Option(OptionQuality))
	if quality == "" {
		quality = "1080p"
	}
	choice, known := recyclarrChoices[quality]
	if !known {
		return stacks.Result{}, stacks.Permanent(fmt.Errorf("the stack's quality is %q; it can be 720p, 1080p or 2160p", quality))
	}
	// A sync may already be running: the image's own daily one, or an
	// earlier attempt of this step that outlived the wait for it. Two at
	// once would both try to create the same custom formats.
	if err := recyclarrWaitIdle(ctx, env); err != nil {
		return stacks.Result{}, err
	}

	config := recyclarrConfig(choice)
	current, err := env.ReadFile(ctx, "recyclarr", recyclarrConfigPath)
	if err != nil || string(current) != string(config) {
		// The image runs as the same account the linuxserver.io images do,
		// and must be able to read its own file.
		if err := env.WriteFile(ctx, "recyclarr", recyclarrConfigPath, config, 0o640, RuntimeUID, RuntimeGID); err != nil {
			return stacks.Result{}, fmt.Errorf("Recyclarr's configuration could not be written: %w", err)
		}
		env.Logf("Recyclarr's configuration was written for %s", quality)
	}

	changed := false
	for _, run := range []struct{ service, instance, title string }{
		{"radarr", recyclarrMovies, "Radarr"},
		{"sonarr", recyclarrSeries, "Sonarr"},
	} {
		// One application at a time, and only the stack's file: a file of
		// the person's own beside it is theirs to run.
		result, err := env.Exec(ctx, "recyclarr", "", []string{"recyclarr", "sync", run.service, "--config", recyclarrConfigPath})
		if err != nil {
			return stacks.Result{}, fmt.Errorf("Recyclarr's sync of %s did not finish in the time given to one command; it carries on in its container and the next attempt waits for it: %w", run.title, err)
		}
		report := recyclarrRead(result.Stdout+"\n"+result.Stderr, run.instance)
		for _, line := range report.summary {
			env.Logf("recyclarr: %s", line)
		}
		if result.ExitCode != 0 || len(report.errors) > 0 || !report.completed {
			reason := strings.Join(report.errors, "; ")
			if reason == "" {
				reason = fmt.Sprintf("it exited with %d and did not report %s as completed", result.ExitCode, run.title)
			}
			return stacks.Result{}, fmt.Errorf("Recyclarr could not sync %s: %s", run.title, reason)
		}
		changed = changed || report.changed
	}

	radarrName, sonarrName := recyclarrProfileName(choice.radarrGuideName), recyclarrProfileName(choice.sonarrGuideName)
	radarrID, err := recyclarrProfileID(ctx, env, arrRadarr, radarrName, choice.radarrTrashID == "")
	if err != nil {
		return stacks.Result{}, err
	}
	sonarrID, err := recyclarrProfileID(ctx, env, arrSonarr, sonarrName, choice.sonarrTrashID == "")
	if err != nil {
		return stacks.Result{}, err
	}

	var message string
	switch {
	case choice.radarrTrashID == "":
		message = fmt.Sprintf("The TRaSH Guides have no %s profile, so Radarr and Sonarr use their built-in %q profile as it is; Recyclarr keeps their quality sizes to the guides", quality, recyclarrBuiltIn720p)
	case changed:
		message = fmt.Sprintf("Recyclarr synced the guides' %s profiles: %q in Radarr and %q in Sonarr", quality, radarrName, sonarrName)
	default:
		message = fmt.Sprintf("The guides' %s profiles were already in step: %q in Radarr and %q in Sonarr", quality, radarrName, sonarrName)
	}
	return stacks.Result{
		Message: message,
		Outputs: map[string]string{
			"quality":           quality,
			"radarrProfileId":   strconv.Itoa(radarrID),
			"radarrProfileName": radarrName,
			"sonarrProfileId":   strconv.Itoa(sonarrID),
			"sonarrProfileName": sonarrName,
		},
	}, nil
}

// recyclarrWaitIdle waits while a sync is running in the container. pgrep
// answers 0 when it finds one and 1 when it finds none; anything else means
// the question could not be asked, and the step goes on.
func recyclarrWaitIdle(ctx context.Context, env stacks.Env) error {
	announced := false
	for {
		result, err := env.Exec(ctx, "recyclarr", "", []string{"pgrep", "-f", "recyclarr sync"})
		if err != nil || result.ExitCode != 0 {
			return nil
		}
		if !announced {
			env.Logf("Recyclarr is already running a sync; waiting for it to finish")
			announced = true
		}
		select {
		case <-ctx.Done():
			return errors.New("Recyclarr was still running an earlier sync when this attempt ran out of time")
		case <-time.After(recyclarrPollInterval):
		}
	}
}

// recyclarrReport is what one sync printed, read for one instance.
type recyclarrReport struct {
	// errors are its [ERR] lines. A file it cannot parse is reported this
	// way with exit status 0, so the status alone is not enough.
	errors    []string
	summary   []string
	completed bool
	changed   bool
}

func recyclarrRead(output, instance string) recyclarrReport {
	var report recyclarrReport
	prefix := instance + ": "
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[ERR]"):
			report.errors = append(report.errors, recyclarrLine(strings.TrimSpace(strings.TrimPrefix(line, "[ERR]")), prefix))
		case strings.HasPrefix(line, "[WRN]"), strings.HasPrefix(line, "[INF]"):
			text := strings.TrimSpace(line[len("[INF]"):])
			if !strings.HasPrefix(text, prefix) {
				continue
			}
			text = recyclarrLine(text, prefix)
			switch {
			case strings.HasPrefix(text, "Completed"):
				report.completed = true
				continue
			case strings.HasPrefix(text, "Processing"):
				continue
			case strings.HasPrefix(text, "Created "), strings.HasPrefix(text, "Updated "), strings.Contains(text, "sizes were synced"), strings.Contains(text, "replaced by Recyclarr"):
				report.changed = true
			}
			report.summary = append(report.summary, text)
		}
	}
	return report
}

// recyclarrLine trims a report line to what a record can hold.
func recyclarrLine(text, prefix string) string {
	text = strings.TrimPrefix(text, prefix)
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

// recyclarrProfileID finds the profile by its name in the application.
func recyclarrProfileID(ctx context.Context, env stacks.Env, app arrApp, name string, builtIn bool) (int, error) {
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		return 0, err
	}
	var profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := client.call(ctx, http.MethodGet, "qualityprofile", nil, &profiles); err != nil {
		return 0, err
	}
	for _, profile := range profiles {
		if profile.Name == name {
			return profile.ID, nil
		}
	}
	if builtIn {
		return 0, stacks.Permanent(fmt.Errorf("%s no longer has its built-in %q quality profile, which 720p uses; choose 1080p or 2160p, or add a profile with that name in %s", app.title, name, app.title))
	}
	return 0, fmt.Errorf("Recyclarr reported a finished sync, but %s has no quality profile named %q", app.title, name)
}
