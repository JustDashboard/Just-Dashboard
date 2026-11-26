package media

// Placeholders for the Radarr, Sonarr, Prowlarr, Bazarr and Recyclarr adapters (D2). The lane that implements them
// deletes this file; nothing here is reachable from a release.

import (
	"context"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

func radarrAuth(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func radarrRootFolder(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func sonarrAuth(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func sonarrRootFolder(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func prowlarrAuth(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func bazarrAuth(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func recyclarrSync(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func radarrDownloadClient(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func sonarrDownloadClient(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func prowlarrApps(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func bazarrLink(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func downloadsVerify(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func prowlarrSources(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}
