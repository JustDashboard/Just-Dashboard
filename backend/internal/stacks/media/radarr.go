package media

import (
	"context"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Radarr's three steps are the shared Servarr operations with its own
// names: the API is /api/v3, the library is Movies, and the qBittorrent
// setting that holds the category is movieCategory.

func radarrAuth(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrAuth(ctx, env, arrRadarr)
}

func radarrRootFolder(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrRootFolder(ctx, env, arrRadarr, "films", MoviesLibrary)
}

func radarrDownloadClient(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	return arrDownloadClient(ctx, env, arrRadarr, "movieCategory", "movies")
}
