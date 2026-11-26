package media

import (
	"context"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Sonarr's three steps are the shared Servarr operations with its own
// names: the API is /api/v3, the library is Shows, and the qBittorrent
// setting that holds the category is tvCategory.

func sonarrAuth(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrAuth(ctx, env, arrSonarr)
}

func sonarrRootFolder(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrRootFolder(ctx, env, arrSonarr, "series", TVLibrary)
}

func sonarrDownloadClient(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrDownloadClient(ctx, env, arrSonarr, "tvCategory", "tv")
}
