package media

// Placeholders for the Jellyfin, Seerr, qBittorrent and storage adapters (D1). The lane that implements them
// deletes this file; nothing here is reachable from a release.

import (
	"context"
	"errors"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

var errNotImplemented = errors.New("this step is not implemented yet")

func storageFolders(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func jellyfinSetup(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func jellyfinLibraries(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func qbittorrentAuth(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func qbittorrentCategories(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func seerrSetup(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func seerrManagers(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func seerrPolicy(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}

func seerrVerify(context.Context, stacks.Env) (stacks.Result, error) {
	return stacks.Result{}, stacks.Permanent(errNotImplemented)
}
