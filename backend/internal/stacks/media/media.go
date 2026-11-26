// Package media is the media server stack: Jellyfin to watch, Seerr to ask
// for something, and in the Complete preset the managers, indexer manager,
// download client, subtitles and quality profiles that fetch what was asked
// for from sources the person adds themselves.
//
// No source, catalogue or credential for one ships here. A Complete stack
// with nothing added can play what is already in its library and says, on
// its page, that it cannot acquire anything until a source is added.
package media

import (
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// ID and Version name the manifest. Version moves when a step's behaviour
// changes in a way an existing stack should be configured again for.
const (
	ID      = "media"
	Version = "1.0.0"
)

// Template ids of the presets: reviewed Compose templates in
// blueprint/builtin, marked with "stack": "media".
const (
	TemplateEssential = "media-essential"
	TemplateComplete  = "media-complete"
)

// Paths every service sees the shared media volume at. Downloads and the
// library are on one volume so a finished download moves into the library
// as a rename (or a hardlink), not a copy.
const (
	DataRoot       = "/data"
	DownloadsRoot  = "/data/downloads"
	MoviesDownload = "/data/downloads/movies"
	TVDownload     = "/data/downloads/tv"
	MediaRoot      = "/data/media"
	MoviesLibrary  = "/data/media/movies"
	TVLibrary      = "/data/media/tv"
)

// The account the linuxserver.io images run their application as (PUID and
// PGID in the templates). Folders the stack makes on the media volume are
// given to it, and writes are checked as it.
const (
	RuntimeUID = 1000
	RuntimeGID = 1000
)

// Internal ports, as the templates declare them.
const (
	PortJellyfin    = 8096
	PortSeerr       = 5055
	PortRadarr      = 7878
	PortSonarr      = 8989
	PortProwlarr    = 9696
	PortQBittorrent = 8080
	PortBazarr      = 6767
)

// Variables the templates generate and hand the services as environment.
const (
	VariableRadarrAPIKey   = "RADARR_API_KEY"
	VariableSonarrAPIKey   = "SONARR_API_KEY"
	VariableProwlarrAPIKey = "PROWLARR_API_KEY"
)

// Option names and the variables that hold them.
const (
	OptionQuality           = "quality"
	OptionSubtitleLanguages = "subtitleLanguages"
	OptionRequestApproval   = "requestApproval"
)

// Credential names.
const (
	CredentialJellyfinAdmin   = "jellyfin-admin"
	CredentialJellyfinAPIKey  = "jellyfin-api-key"
	CredentialQBittorrent     = "qbittorrent-admin"
	CredentialRadarrAdmin     = "radarr-admin"
	CredentialSonarrAdmin     = "sonarr-admin"
	CredentialProwlarrAdmin   = "prowlarr-admin"
	CredentialBazarrAdmin     = "bazarr-admin"
	CredentialBazarrAPIKey    = "bazarr-api-key"
	CredentialSeerrAPIKey     = "seerr-api-key"
	AdminUsername             = "admin"
	generatedPasswordLength   = 24
	actionMediaSource         = "media-source"
	actionJellyfinTakenOver   = "jellyfin-owner"
	actionSeerrTakenOver      = "seerr-owner"
	actionQBittorrentPassword = "qbittorrent-password"
)

// Manifest is the media stack.
func Manifest() *stacks.Manifest {
	return &stacks.Manifest{
		ID:      ID,
		Version: Version,
		Title:   "Media server",
		Description: "Jellyfin to watch your films and series, and Seerr for people to ask for more. " +
			"The Complete preset adds the managers, download client, subtitles and quality profiles " +
			"that fetch what was asked for, from sources you add yourself.",
		Presets: []stacks.Preset{
			{
				ID:    "complete",
				Title: "Complete",
				Description: "Everything connected: requests in Seerr are searched for by Radarr and Sonarr " +
					"through the sources you add to Prowlarr, downloaded by qBittorrent, given subtitles " +
					"by Bazarr and quality profiles by Recyclarr, and land in Jellyfin.",
				Template:    TemplateComplete,
				Services:    []string{"jellyfin", "seerr", "radarr", "sonarr", "prowlarr", "qbittorrent", "bazarr", "recyclarr"},
				Recommended: true,
				Limitations: []string{
					"Nothing can be downloaded until you add a source you are allowed to use in Prowlarr. None is included, and no films or series come with the stack.",
					"Media goes into the stack's own volume. An existing library elsewhere on this server is not mounted.",
				},
			},
			{
				ID:    "essential",
				Title: "Essential",
				Description: "Jellyfin and Seerr for a library you already have. People can browse and ask " +
					"for titles, but nothing fetches what they ask for.",
				Template: TemplateEssential,
				Services: []string{"jellyfin", "seerr"},
				Limitations: []string{
					"A request cannot be fulfilled automatically: there is no manager or download client. Requests wait for you to add the title to the library yourself.",
					"Media goes into the stack's own volume. An existing library elsewhere on this server is not mounted.",
				},
			},
		},
		Services: []stacks.ServiceInfo{
			{Name: "jellyfin", Title: "Jellyfin", Purpose: "Plays your films and series, with apps for every screen", Port: PortJellyfin, Access: stacks.AccessPublic, DocsURL: "https://jellyfin.org/docs/"},
			{Name: "seerr", Title: "Seerr", Purpose: "Where people search for a title and ask for it", Port: PortSeerr, Access: stacks.AccessPrivate, DocsURL: "https://docs.seerr.dev/"},
			{Name: "radarr", Title: "Radarr", Purpose: "Finds, downloads and files films", Port: PortRadarr, Access: stacks.AccessPrivate, DocsURL: "https://wiki.servarr.com/radarr"},
			{Name: "sonarr", Title: "Sonarr", Purpose: "Finds, downloads and files series", Port: PortSonarr, Access: stacks.AccessPrivate, DocsURL: "https://wiki.servarr.com/sonarr"},
			{Name: "prowlarr", Title: "Prowlarr", Purpose: "Keeps the sources you add and shares them with Radarr and Sonarr", Port: PortProwlarr, Access: stacks.AccessPrivate, DocsURL: "https://wiki.servarr.com/prowlarr"},
			{Name: "qbittorrent", Title: "qBittorrent", Purpose: "Downloads what Radarr and Sonarr send it", Port: PortQBittorrent, Access: stacks.AccessPrivate, DocsURL: "https://github.com/qbittorrent/qBittorrent/wiki"},
			{Name: "bazarr", Title: "Bazarr", Purpose: "Fetches subtitles in your languages", Port: PortBazarr, Access: stacks.AccessPrivate, DocsURL: "https://wiki.bazarr.media/"},
			{Name: "recyclarr", Title: "Recyclarr", Purpose: "Keeps Radarr's and Sonarr's quality profiles in step with your choice", Access: stacks.AccessWorker, DocsURL: "https://recyclarr.dev/"},
		},
		Options: []stacks.Option{
			{Name: OptionQuality, Variable: "MEDIA_QUALITY", Default: "1080p"},
			{Name: OptionSubtitleLanguages, Variable: "MEDIA_SUBTITLE_LANGUAGES", Default: "en"},
			{Name: OptionRequestApproval, Variable: "MEDIA_REQUEST_APPROVAL", Default: "admin"},
		},
		Credentials: []stacks.CredentialSpec{
			{Name: CredentialJellyfinAdmin, Service: "jellyfin", Label: "Jellyfin administrator (also signs in to Seerr)", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialQBittorrent, Service: "qbittorrent", Label: "qBittorrent web interface", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialRadarrAdmin, Service: "radarr", Label: "Radarr", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialSonarrAdmin, Service: "sonarr", Label: "Sonarr", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialProwlarrAdmin, Service: "prowlarr", Label: "Prowlarr", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialBazarrAdmin, Service: "bazarr", Label: "Bazarr", Username: AdminUsername, Length: generatedPasswordLength},
			{Name: CredentialJellyfinAPIKey, Service: "jellyfin", Label: "Jellyfin API key the stack uses", Internal: true},
			{Name: CredentialBazarrAPIKey, Service: "bazarr", Label: "Bazarr API key the stack uses", Internal: true},
			{Name: CredentialSeerrAPIKey, Service: "seerr", Label: "Seerr API key the stack uses", Internal: true},
		},
		Steps: steps(),
	}
}

// steps is the whole graph in one place, so its order is reviewed as one
// thing: infrastructure, then each application's own setup, then the quality
// profiles (before Seerr is told which one to use), then the links between
// applications, then the checks across them.
func steps() []stacks.Step {
	const (
		short = 2 * time.Minute
		long  = 5 * time.Minute
	)
	return []stacks.Step{
		// Initialize: each application on its own.
		{ID: "storage.folders", Service: "jellyfin", Title: "Make the download and library folders", Stage: stacks.StageInitialize,
			Timeout: short, Attempts: 5, Run: storageFolders},
		{ID: "jellyfin.setup", Service: "jellyfin", Title: "Finish Jellyfin's first-run setup and create its administrator", Stage: stacks.StageInitialize,
			Timeout: long, Attempts: 8, Run: jellyfinSetup},
		{ID: "jellyfin.libraries", Service: "jellyfin", Title: "Create the Movies and Shows libraries", Stage: stacks.StageInitialize,
			After: []string{"jellyfin.setup", "storage.folders"}, Timeout: short, Attempts: 5, Run: jellyfinLibraries},
		{ID: "qbittorrent.auth", Service: "qbittorrent", Title: "Give qBittorrent's web interface its own password", Stage: stacks.StageInitialize,
			Timeout: short, Attempts: 8, Run: qbittorrentAuth},
		{ID: "qbittorrent.categories", Service: "qbittorrent", Title: "Set download folders and the movies and tv categories", Stage: stacks.StageInitialize,
			After: []string{"qbittorrent.auth", "storage.folders"}, Timeout: short, Attempts: 5, Run: qbittorrentCategories},
		{ID: "radarr.auth", Service: "radarr", Title: "Secure Radarr with its own sign-in", Stage: stacks.StageInitialize,
			Timeout: long, Attempts: 8, Run: radarrAuth},
		{ID: "radarr.rootfolder", Service: "radarr", Title: "Point Radarr at the Movies library", Stage: stacks.StageInitialize,
			After: []string{"radarr.auth", "storage.folders"}, Timeout: short, Attempts: 5, Run: radarrRootFolder},
		{ID: "sonarr.auth", Service: "sonarr", Title: "Secure Sonarr with its own sign-in", Stage: stacks.StageInitialize,
			Timeout: long, Attempts: 8, Run: sonarrAuth},
		{ID: "sonarr.rootfolder", Service: "sonarr", Title: "Point Sonarr at the Shows library", Stage: stacks.StageInitialize,
			After: []string{"sonarr.auth", "storage.folders"}, Timeout: short, Attempts: 5, Run: sonarrRootFolder},
		{ID: "prowlarr.auth", Service: "prowlarr", Title: "Secure Prowlarr with its own sign-in", Stage: stacks.StageInitialize,
			Timeout: long, Attempts: 8, Run: prowlarrAuth},
		{ID: "bazarr.auth", Service: "bazarr", Title: "Secure Bazarr with its own sign-in", Stage: stacks.StageInitialize,
			Timeout: long, Attempts: 8, Run: bazarrAuth},

		// Link: quality profiles first, since Seerr is told which one to use.
		{ID: "recyclarr.sync", Service: "recyclarr", Title: "Sync quality profiles into Radarr and Sonarr", Stage: stacks.StageLink,
			Needs: []string{"radarr", "sonarr"}, After: []string{"radarr.auth", "sonarr.auth"}, Timeout: 10 * time.Minute, Attempts: 4, Run: recyclarrSync},
		{ID: "radarr.downloadclient", Service: "radarr", Title: "Connect Radarr to qBittorrent", Stage: stacks.StageLink,
			Needs: []string{"qbittorrent"}, After: []string{"radarr.auth", "qbittorrent.categories"}, Timeout: short, Attempts: 5, Run: radarrDownloadClient},
		{ID: "sonarr.downloadclient", Service: "sonarr", Title: "Connect Sonarr to qBittorrent", Stage: stacks.StageLink,
			Needs: []string{"qbittorrent"}, After: []string{"sonarr.auth", "qbittorrent.categories"}, Timeout: short, Attempts: 5, Run: sonarrDownloadClient},
		{ID: "prowlarr.apps", Service: "prowlarr", Title: "Share Prowlarr's sources with Radarr and Sonarr", Stage: stacks.StageLink,
			Needs: []string{"radarr", "sonarr"}, After: []string{"prowlarr.auth", "radarr.auth", "sonarr.auth"}, Timeout: short, Attempts: 5, Run: prowlarrApps},
		{ID: "bazarr.link", Service: "bazarr", Title: "Connect Bazarr to Radarr and Sonarr in your subtitle languages", Stage: stacks.StageLink,
			Needs: []string{"radarr", "sonarr"}, After: []string{"bazarr.auth", "radarr.rootfolder", "sonarr.rootfolder"}, Timeout: short, Attempts: 5, Run: bazarrLink},
		{ID: "seerr.setup", Service: "seerr", Title: "Sign Seerr in to Jellyfin and sync its libraries", Stage: stacks.StageLink,
			After: []string{"jellyfin.libraries"}, Timeout: long, Attempts: 8, Run: seerrSetup},
		{ID: "seerr.managers", Service: "seerr", Title: "Connect Seerr to Radarr and Sonarr", Stage: stacks.StageLink,
			Needs: []string{"radarr", "sonarr"}, After: []string{"seerr.setup", "radarr.rootfolder", "sonarr.rootfolder", "recyclarr.sync"},
			Timeout: short, Attempts: 5, Run: seerrManagers},
		{ID: "seerr.policy", Service: "seerr", Title: "Apply who may request and whether requests need approval", Stage: stacks.StageLink,
			After: []string{"seerr.setup"}, Timeout: short, Attempts: 5, Run: seerrPolicy},

		// Verify: across applications, read back.
		{ID: "downloads.verify", Service: "qbittorrent", Title: "Test Radarr's and Sonarr's connection to qBittorrent", Stage: stacks.StageVerify,
			Needs: []string{"radarr", "sonarr"}, After: []string{"radarr.downloadclient", "sonarr.downloadclient"}, Timeout: short, Attempts: 3, Run: downloadsVerify},
		{ID: "seerr.verify", Service: "seerr", Title: "Test Seerr's connections", Stage: stacks.StageVerify,
			After: []string{"seerr.managers", "seerr.policy"}, Timeout: short, Attempts: 3, Run: seerrVerify},
		{ID: "prowlarr.sources", Service: "prowlarr", Title: "Check for a media source", Stage: stacks.StageVerify,
			After: []string{"prowlarr.apps"}, Timeout: short, Attempts: 3, Run: prowlarrSources},
	}
}
