package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountDeployRoutes(r chi.Router) {
	r.Route("/deploy", func(r chi.Router) {
		r.Use(s.retiredImportReadOnly)
		s.mountBlueprintRoutes(r)
		s.mountGameRoutes(r)
		s.mountGitHubAppRoutes(r)
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodGet, "/drafts", s.handle(s.handleDeploymentDraftList))
			r.Method(http.MethodGet, "/drafts/{draft}", s.handle(s.handleDeploymentDraftGet))
			r.Method(http.MethodPut, "/drafts/{draft}", s.handle(s.handleDeploymentDraftSave))
			r.Method(http.MethodDelete, "/drafts/{draft}", s.handle(s.handleDeploymentDraftDiscard))
			r.Method(http.MethodPost, "/drafts/{draft}/detect", s.handle(s.handleDeploymentDraftDetect))
			r.Method(http.MethodPost, "/drafts/{draft}/preflight", s.handle(s.handleDeploymentDraftPreflight))
		})
		r.Method(http.MethodGet, "/", s.handle(s.handleDeployList))
		r.Method(http.MethodGet, "/hostname", s.handle(s.handleDeploymentHostname))
		// The OpenAPI description of this API (deploy_openapi.go): static,
		// for any signed-in account and any key, and registered before /{id}
		// for the reason /hostname is.
		r.Method(http.MethodGet, "/openapi.json", s.handle(s.handleDeployOpenAPIJSON))
		r.Method(http.MethodGet, "/openapi.yaml", s.handle(s.handleDeployOpenAPIYAML))
		// Static, and registered before /{id} for the same reason /hostname
		// is: chi prefers the literal segment, so the fleet's counts never
		// read as a project called "pull-requests".
		r.Method(http.MethodGet, "/pull-requests", s.handle(s.handleDeploymentPullRequestSummary))
		// Every environment's right-sizing verdict, the furthest off first.
		r.Method(http.MethodGet, "/sizing", s.handle(s.handleDeploymentFleetSizing))
		// Masked like an environment's variable list, and read by whoever
		// may read that; a value leaves only through the audited reveal.
		r.Method(http.MethodGet, "/shared-variables", s.handle(s.handleSharedVariables))
		r.Method(http.MethodGet, "/{id}/shared-variables", s.handle(s.handleSharedVariables))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleDeployGet))
		r.Method(http.MethodGet, "/{id}/pull-requests", s.handle(s.handleDeploymentPullRequests))
		r.Method(http.MethodGet, "/{id}/pull-requests/{number}/checks", s.handle(s.handleDeploymentPullRequestChecks))
		r.Method(http.MethodGet, "/{id}/preview-frame", s.handle(s.handleDeploymentPreviewFrame))
		r.Method(http.MethodGet, "/{id}/favicon", s.handle(s.handleDeploymentFavicon))
		r.Method(http.MethodGet, "/{id}/runs", s.handle(s.handleDeployRuns))
		r.Method(http.MethodGet, "/{id}/runs/{run}", s.handle(s.handleDeploymentRunGet))
		r.Method(http.MethodGet, "/{id}/runs/{run}/logs", s.handle(s.handleDeploymentRunLogs))
		// The transcript the stream sends, read from a sequence number on, for
		// a client without the stream's socket: jd logs --follow.
		r.Method(http.MethodGet, "/{id}/runs/{run}/transcript", s.handle(s.handleDeploymentRunTranscript))
		r.Method(http.MethodGet, "/{id}/runs/{run}/metrics", s.handle(s.handleDeploymentRunMetrics))
		r.Method(http.MethodGet, "/{id}/runs/{run}/stream", s.handle(s.handleDeploymentRunStream))
		r.Method(http.MethodGet, "/{id}/runs/{run}/settings-drift", s.handle(s.handleDeploymentRunSettingsDrift))
		r.Method(http.MethodGet, "/{id}/runs/{run}/diagnosis", s.handle(s.handleDeploymentRunDiagnosis))
		r.Method(http.MethodGet, "/{id}/requests", s.handle(s.handleDeploymentRequests))
		r.Method(http.MethodGet, "/{id}/requests/stream", s.handle(s.handleDeploymentRequestStream))
		r.Method(http.MethodGet, "/{id}/requests/export", s.handle(s.handleDeploymentRequestExport))
		r.Method(http.MethodGet, "/{id}/runs/{run}/traffic", s.handle(s.handleDeploymentRunTraffic))
		r.Method(http.MethodGet, "/{id}/lifecycle", s.handle(s.handleDeploymentLifecycle))
		r.Method(http.MethodGet, "/{id}/lifecycle/stream", s.handle(s.handleDeploymentLifecycleStream))
		r.Method(http.MethodGet, "/{id}/availability", s.handle(s.handleDeploymentAvailability))
		r.Method(http.MethodGet, "/{id}/alerts", s.handle(s.handleTrafficAlertList))
		r.Method(http.MethodGet, "/traffic", s.handle(s.handleDeploymentTrafficPulse))
		r.Method(http.MethodGet, "/{id}/commits", s.handle(s.handleDeployCommits))
		r.Method(http.MethodGet, "/{id}/env", s.handle(s.handleDeployEnvList))
		r.Method(http.MethodGet, "/{id}/environments", s.handle(s.handleDeploymentEnvironments))
		r.Method(http.MethodGet, "/{id}/environments/{env}/releases", s.handle(s.handleDeploymentReleases))
		r.Method(http.MethodGet, "/{id}/environments/{env}/promotion", s.handle(s.handleDeploymentPromotionPreview))
		r.Method(http.MethodGet, "/{id}/operations", s.handle(s.handleDeploymentOperations))
		r.Method(http.MethodGet, "/{id}/insights", s.handle(s.handleDeploymentInsights))
		r.Method(http.MethodGet, "/{id}/environments/{env}/releases/{release}/comparison", s.handle(s.handleDeploymentReleaseComparison))
		r.Method(http.MethodGet, "/{id}/environments/{env}/variables", s.handle(s.handleDeploymentVariables))
		r.Method(http.MethodGet, "/{id}/environments/{env}/pending", s.handle(s.handleDeploymentPending))
		r.Method(http.MethodGet, "/{id}/environments/{env}/configuration", s.handle(s.handleDeploymentConfiguration))
		// A read: applying it is the configuration PUT above, with its audit.
		r.Method(http.MethodGet, "/{id}/environments/{env}/sizing", s.handle(s.handleDeploymentSizing))
		r.Method(http.MethodGet, "/{id}/environments/{env}/maintenance", s.handle(s.handleDeploymentMaintenance))
		r.Method(http.MethodGet, "/{id}/environments/{env}/pages/{page}", s.handle(s.handleDeploymentPageGet))
		r.Method(http.MethodGet, "/{id}/environments/{env}/triggers", s.handle(s.handleDeploymentTriggers))
		r.Method(http.MethodGet, "/{id}/environments/{env}/git-watch", s.handle(s.handleDeploymentGitWatch))
		r.Method(http.MethodGet, "/{id}/environments/{env}/rollback-watch", s.handle(s.handleDeploymentRollbackWatch))
		r.Method(http.MethodGet, "/{id}/environments/{env}/canary", s.handle(s.handleDeploymentCanary))
		s.mountDeploymentSleepReads(r)
		r.Method(http.MethodGet, "/{id}/environments/{env}/rehearsal", s.handle(s.handleDeploymentRehearsal))
		r.Method(http.MethodGet, "/{id}/environments/{env}/vulnerabilities", s.handle(s.handleDeploymentImageScans))
		r.Method(http.MethodGet, "/{id}/environments/{env}/database-links", s.handle(s.handleDeploymentDatabaseLinks))
		// The copies of a release's data an environment holds: what they are
		// of and how long they are kept, never how to sign in to one.
		r.Method(http.MethodGet, "/{id}/environments/{env}/data-copies", s.handle(s.handleDeploymentDataCopies))
		r.Method(http.MethodGet, "/{id}/environments/{env}/database-addresses", s.handle(s.handleDeploymentDatabaseAddresses))
		r.Method(http.MethodGet, "/{id}/environments/{env}/schedules", s.handle(s.handleDeploymentSchedules))
		r.Method(http.MethodGet, "/{id}/environments/{env}/schedules/{schedule}/runs", s.handle(s.handleDeploymentScheduleRuns))
		r.Method(http.MethodGet, "/{id}/previews", s.handle(s.handleDeploymentPreviews))
		r.Method(http.MethodGet, "/{id}/previews/approvals", s.handle(s.handleDeploymentPreviewApprovals))
		r.Method(http.MethodGet, "/notifications", s.handle(s.handleDeploymentNotifications))
		r.Method(http.MethodGet, "/notifications/options", s.handle(s.handleDeploymentNotificationOptions))
		r.Method(http.MethodGet, "/notifications/{channel}/deliveries", s.handle(s.handleDeploymentNotificationDeliveries))
		s.mountLogDrainReads(r)
		s.mountSecretStoreReads(r)

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{id}/run", s.handle(s.handleDeployRun))
			r.Method(http.MethodPost, "/{id}/environments/{env}/runs", s.handle(s.handleDeploymentRunCreate))
			// Promoting is deploying production: same capability, same
			// queue, same checks and cutover, and a key may do it as it
			// may deploy — an ordinary key here, a deploy key by its line
			// in deployKeyRoutes (deploy_keys.go).
			r.Method(http.MethodPost, "/{id}/environments/{env}/promote", s.handle(s.handleDeploymentPromote))
			r.Method(http.MethodPost, "/{id}/runs/{run}/cancel", s.handle(s.handleDeploymentRunCancel))
			r.Method(http.MethodPost, "/{id}/runs/{run}/retry", s.handle(s.handleDeploymentRunRetry))
			// Keeping the new release only removes a container nothing routes
			// to; it is the same control a deploy is.
			r.Method(http.MethodPost, "/{id}/environments/{env}/rollback-watch/keep", s.handle(s.handleDeploymentKeepRelease))
			// A canary is decided by the control a deploy is: promoting it is
			// the cutover the deploy would have made, and aborting it removes
			// a candidate with only its share of the traffic, the live release
			// untouched.
			r.Method(http.MethodPost, "/{id}/environments/{env}/canary/promote", s.handle(s.handleDeploymentCanaryPromote))
			r.Method(http.MethodPost, "/{id}/environments/{env}/canary/abort", s.handle(s.handleDeploymentCanaryAbort))
			s.mountDeploymentSleepWake(r)
			// Asking about a failure takes what asking the SQL editor's
			// assistant takes: it spends the install's model budget.
			r.Method(http.MethodPost, "/{id}/runs/{run}/diagnosis", s.handle(s.handleDeploymentRunDiagnose))
			// The operator reads with the person's leave a call at a time and
			// proposes actions their press runs, so it is a session's: there
			// is nobody to ask behind a key.
			r.With(httpx.RequireSession).Method(http.MethodPost, "/{id}/assistant", s.handle(s.handleDeploymentOperator))
			// A scan reads the release's images in a container with no
			// network; it costs the host a few minutes of one CPU, as a
			// build does, and changes nothing the release runs.
			r.Method(http.MethodPost, "/{id}/environments/{env}/vulnerabilities/scan", s.handle(s.handleDeploymentImageScanRequest))
			// Merging and commenting speak on GitHub as the dashboard's own
			// account, which is why an API token may not: the same rule as
			// the Git page's write routes, with the session requirement the
			// deploy page's other GitHub-facing verbs carry.
			r.With(httpx.RequireSession).Method(http.MethodPost, "/{id}/pull-requests/{number}/merge", s.handle(s.handleDeploymentPullRequestMerge))
			r.With(httpx.RequireSession).Method(http.MethodPost, "/{id}/pull-requests/{number}/comment", s.handle(s.handleDeploymentPullRequestComment))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireSession)
				r.Method(http.MethodPost, "/drafts", s.handle(s.handleDeploymentDraftCreate))
				r.Method(http.MethodPost, "/drafts/{draft}/commit", s.handle(s.handleDeploymentDraftCommit))
				r.Method(http.MethodPost, "/import/preview", s.handle(s.handleDeploymentImportPreview))
				r.Method(http.MethodPost, "/import/adopt", s.handle(s.handleDeploymentImportAdopt))
				r.Method(http.MethodPost, "/game/import/preview", s.handle(s.handleGameImportPreview))
				r.Method(http.MethodPost, "/{id}/unarchive", s.handle(s.handleDeploymentUnarchive))
				r.Method(http.MethodPut, "/{id}/environments/{env}/releases/{release}/pin", s.handle(s.handleDeploymentReleasePin))
				r.Method(http.MethodPost, "/{id}/previews/approvals/{approval}/reject", s.handle(s.handleDeploymentPreviewReject))
				r.Method(http.MethodGet, "/{id}/environments/{env}/triggers/{trigger}/deliveries", s.handle(s.handleDeploymentTriggerDeliveries))
				r.Method(http.MethodPost, "/{id}/environments/{env}/triggers/{trigger}/rotate-secret", s.handle(s.handleDeploymentTriggerRotateSecret))
				r.Method(http.MethodGet, "/{id}/environments/{env}/variables/{name}/reveal", s.handle(s.handleDeploymentVariableReveal))
				r.Method(http.MethodPut, "/{id}/environments/{env}/variables/{name}", s.handle(s.handleDeploymentVariablePut))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/variables/{name}", s.handle(s.handleDeploymentVariableDelete))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/import", s.handle(s.handleDeploymentDotenvImport))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/{name}/generate", s.handle(s.handleDeploymentVariableGenerate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/{name}/rotate", s.handle(s.handleDeploymentVariableRotate))
				// A shared variable is written and revealed on the terms an
				// environment's own variable is: an administrator, in a session.
				r.Method(http.MethodPut, "/shared-variables/{name}", s.handle(s.handleSharedVariablePut))
				r.Method(http.MethodGet, "/shared-variables/{name}/reveal", s.handle(s.handleSharedVariableReveal))
				r.Method(http.MethodPut, "/{id}/shared-variables/{name}", s.handle(s.handleSharedVariablePut))
				r.Method(http.MethodGet, "/{id}/shared-variables/{name}/reveal", s.handle(s.handleSharedVariableReveal))
				r.Method(http.MethodPut, "/{id}/environments/{env}/configuration", s.handle(s.handleDeploymentConfigurationSave))
				// What every visitor is shown when the route answers with one
				// of its own pages: written as the configuration is.
				r.Method(http.MethodPut, "/{id}/environments/{env}/pages/{page}", s.handle(s.handleDeploymentPagePut))
				r.Method(http.MethodPut, "/{id}/environments/{env}/source", s.handle(s.handleDeploymentSourceUpdate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/detect", s.handle(s.handleDeploymentDetect))
				r.Method(http.MethodPut, "/{id}/environments/{env}/git-policy", s.handle(s.handleDeploymentGitPolicyPut))
				r.Method(http.MethodPut, "/{id}/environments/{env}/rollback-policy", s.handle(s.handleDeploymentRollbackPolicyPut))
				// Adding staging writes configuration and copies variables,
				// so it is an administrator's, in a session, like saving them.
				r.Method(http.MethodPost, "/{id}/environments", s.handle(s.handleDeploymentStagingCreate))
				r.Method(http.MethodPut, "/{id}/environments/{env}/rehearsal", s.handle(s.handleDeploymentRehearsalPut))
				r.Method(http.MethodPut, "/{id}/environments/{env}/vulnerabilities", s.handle(s.handleDeploymentImageScanPolicyPut))
				// What a linked database gives the environment's previews: a
				// second copy of production's data on a preview's network, so
				// an administrator's in a session, like the rehearsal's terms.
				r.Method(http.MethodPut, "/{id}/environments/{env}/database-links/{connection}/previews", s.handle(s.handleDeploymentPreviewBranchPolicyPut))
				// A copy of a release's data beside production: a second copy
				// of production's data, whose address carries a password, so
				// an administrator's in a session. Nothing production holds is
				// written, so none of it is destructive.
				r.Method(http.MethodPost, "/{id}/environments/{env}/releases/{release}/data-copies", s.handle(s.handleDeploymentDataCopyCreate))
				r.Method(http.MethodGet, "/{id}/environments/{env}/data-copies/{copy}/url", s.handle(s.handleDeploymentDataCopyURL))
				r.Method(http.MethodPost, "/{id}/environments/{env}/data-copies/{copy}/extend", s.handle(s.handleDeploymentDataCopyExtend))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/data-copies/{copy}", s.handle(s.handleDeploymentDataCopyRemove))
				r.Method(http.MethodGet, "/{id}/environments/{env}/data-copies/{copy}/comparison", s.handle(s.handleDeploymentDataCopyComparison))
				r.Method(http.MethodPost, "/{id}/previews/approvals/{approval}/approve", s.handle(s.handleDeploymentPreviewApprove))
				// Testing a pull request is the approval of that head, so it
				// sits with the approval route it stands in for.
				r.Method(http.MethodPost, "/{id}/pull-requests/{number}/preview", s.handle(s.handleDeploymentPullRequestPreview))
				r.Method(http.MethodPost, "/{id}/pull-requests/{number}/preview/branches/{connection}/refresh", s.handle(s.handleDeploymentPreviewBranchRefresh))
				r.Method(http.MethodPost, "/{id}/duplicate", s.handle(s.handleDeploymentDuplicate))
				r.Method(http.MethodPost, "/{id}/removal-plan", s.handle(s.handleDeploymentRemovalPlan))
				r.Method(http.MethodGet, "/credentials", s.handle(s.handleDeploymentCredentials))
				r.Method(http.MethodPost, "/credentials", s.handle(s.handleDeploymentCredentialCreate))
				r.Method(http.MethodPut, "/credentials/{id}", s.handle(s.handleDeploymentCredentialUpdate))
				r.Method(http.MethodPost, "/credentials/{id}/test", s.handle(s.handleDeploymentCredentialTest))
				r.Method(http.MethodPost, "/{id}/environments/{env}/triggers", s.handle(s.handleDeploymentTriggerCreate))
				r.Method(http.MethodPut, "/{id}/environments/{env}/triggers/{trigger}", s.handle(s.handleDeploymentTriggerUpdate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/triggers/{trigger}/test", s.handle(s.handleDeploymentTriggerTest))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/triggers/{trigger}", s.handle(s.handleDeploymentTriggerDelete))
				r.Method(http.MethodPost, "/{id}/environments/{env}/watch-paths/simulate", s.handle(s.handleDeploymentWatchPathSimulate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/schedules", s.handle(s.handleDeploymentScheduleCreate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/schedules/test", s.handle(s.handleDeploymentScheduleTest))
				r.Method(http.MethodPut, "/{id}/environments/{env}/schedules/{schedule}", s.handle(s.handleDeploymentScheduleUpdate))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/schedules/{schedule}", s.handle(s.handleDeploymentScheduleDelete))
				r.Method(http.MethodPost, "/{id}/alerts", s.handle(s.handleTrafficAlertCreate))
				r.Method(http.MethodPut, "/{id}/alerts/{alert}", s.handle(s.handleTrafficAlertUpdate))
				r.Method(http.MethodDelete, "/{id}/alerts/{alert}", s.handle(s.handleTrafficAlertDelete))
				r.Method(http.MethodPost, "/{id}/alerts/{alert}/test", s.handle(s.handleTrafficAlertTest))
				r.Method(http.MethodPost, "/notifications", s.handle(s.handleDeploymentNotificationCreate))
				r.Method(http.MethodPut, "/notifications/{channel}", s.handle(s.handleDeploymentNotificationUpdate))
				r.Method(http.MethodPost, "/notifications/{channel}/test", s.handle(s.handleDeploymentNotificationTest))
				r.Method(http.MethodPut, "/notifications/{channel}/enabled", s.handle(s.handleDeploymentNotificationEnabled))
				r.Method(http.MethodDelete, "/notifications/{channel}", s.handle(s.handleDeploymentNotificationDelete))
				s.mountLogDrainWrites(r)
				s.mountSecretStoreWrites(r)
			})
			r.Method(http.MethodPost, "/", s.handle(s.handleDeployCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleDeployUpdate))
			r.Method(http.MethodPost, "/{id}/rotate-secret", s.handle(s.handleDeployRotateSecret))
			r.Method(http.MethodPut, "/{id}/env", s.handle(s.handleDeployEnvSet))
			r.Method(http.MethodGet, "/{id}/env/reveal", s.handle(s.handleDeployEnvReveal))
			r.Method(http.MethodDelete, "/{id}/env/{key}", s.handle(s.handleDeployEnvDelete))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/permanent", s.handle(s.handleDeployPurge))
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDeployDelete))
			r.Method(http.MethodPost, "/{id}/archive", s.handle(s.handleDeploymentArchive))
			r.Method(http.MethodPost, "/{id}/remove-managed", s.handle(s.handleDeploymentRemoveManaged))
			r.Method(http.MethodPost, "/{id}/rollback", s.handle(s.handleDeployRollback))
			r.Method(http.MethodPost, "/{id}/environments/{env}/rollback", s.handle(s.handleDeploymentReleaseRollback))
			// Maintenance takes the application away from its visitors, as
			// stopping it does and as a Proxy site's maintenance does, and
			// sits behind the same gate as both.
			r.Method(http.MethodPut, "/{id}/environments/{env}/maintenance", s.handle(s.handleDeploymentMaintenanceSet))
			// Switching traffic back to the warm release takes the live one out
			// of service, exactly as a rollback from the list does.
			r.Method(http.MethodPost, "/{id}/environments/{env}/rollback-watch/rollback", s.handle(s.handleDeploymentInstantRollback))
			// Removing staging takes its containers, and its volumes when
			// asked, off the host: destructive, ordinary confirmation, since
			// staging is added again in a minute and production is untouched.
			r.With(httpx.RequireSession).Method(http.MethodDelete, "/{id}/environments/{env}", s.handle(s.handleDeploymentStagingRemove))
			// A saved credential is routine and recoverable exactly like a
			// saved DNS-provider credential: destructive, but never a typed
			// phrase, since the secret is pasted again in a minute.
			r.Method(http.MethodDelete, "/credentials/{id}", s.handle(s.handleDeploymentCredentialDelete))
			// Removing a shared variable is a saved credential's case: refused
			// while anything reads it, and typed again in a minute otherwise.
			r.With(httpx.RequireSession).Method(http.MethodDelete, "/shared-variables/{name}", s.handle(s.handleSharedVariableDelete))
			r.With(httpx.RequireSession).Method(http.MethodDelete, "/{id}/shared-variables/{name}", s.handle(s.handleSharedVariableDelete))
			// Closing a preview removes its container, volumes and address:
			// destructive, with an ordinary confirmation, since the pull
			// request can be tested again in a minute.
			r.With(httpx.RequireSession).Method(http.MethodPost, "/{id}/pull-requests/{number}/preview/close", s.handle(s.handleDeploymentPullRequestClose))
			// Removing a secret store is a saved credential's case: refused
			// while a variable reads it, and entered again in a minute.
			s.mountSecretStoreRemoval(r)
		})
	})
}

func mapDeployError(err error) error {
	// A preview's promotion is refused in words that name the rule: the
	// request was understood, and this preview may not go where it asked.
	var previewRefusal *deploy.PreviewPromotionRefusal
	if errors.As(err, &previewRefusal) {
		return httpx.Err(http.StatusUnprocessableEntity, previewRefusal.Code, previewRefusal.Message)
	}
	switch {
	case errors.Is(err, deploy.ErrPreviewApproval), errors.Is(err, deploy.ErrPreviewIsolation), errors.Is(err, deploy.ErrPreviewCleanupPending), errors.Is(err, deploy.ErrGitManualOnly), errors.Is(err, deploy.ErrGitPolicyConflict), errors.Is(err, deploy.ErrPromotionOnly), errors.Is(err, deploy.ErrStagingIsolation):
		return mapAutomationError(err)
	case errors.Is(err, deploy.ErrStagingRequired):
		return httpx.Err(http.StatusConflict, "staging_required", err.Error())
	case errors.Is(err, deploy.ErrStagingExists):
		return httpx.Err(http.StatusConflict, "staging_exists", err.Error())
	case errors.Is(err, deploy.ErrStagingUnsupported):
		return httpx.Err(http.StatusConflict, "staging_unsupported", err.Error())
	case errors.Is(err, deploy.ErrStagingDomainTaken):
		return httpx.Err(http.StatusConflict, "domain_taken", err.Error())
	case errors.Is(err, deploy.ErrStagingDomainInvalid):
		return httpx.Err(http.StatusBadRequest, "invalid_domain", err.Error())
	case errors.Is(err, deploy.ErrNotStaging):
		return httpx.Err(http.StatusConflict, "not_staging", err.Error())
	case errors.Is(err, deploy.ErrPromotionOnlyNeedsStaging):
		return httpx.Err(http.StatusConflict, "promotion_only_requires_staging", err.Error())
	case errors.Is(err, deploy.ErrPromotionImageMissing):
		return httpx.Err(http.StatusConflict, "promotion_image_missing", err.Error())
	case errors.Is(err, deploy.ErrPromotionSource):
		return httpx.Err(http.StatusConflict, "promotion_source", err.Error())
	case errors.Is(err, deploy.ErrPromotionTarget):
		return httpx.Err(http.StatusConflict, "promotion_target", err.Error())
	case errors.Is(err, deploy.ErrPromotionIncompatible):
		return httpx.Err(http.StatusConflict, "promotion_incompatible", err.Error())
	case errors.Is(err, deploy.ErrBuilderUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "builder_unavailable", err.Error())
	case errors.Is(err, deploy.ErrArchiveRequired):
		return httpx.Err(http.StatusConflict, "deployment_not_archived", err.Error())
	case errors.Is(err, deploy.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, deploy.ErrAlreadyDeploying):
		return httpx.Err(http.StatusConflict, "already_running", err.Error())
	case errors.Is(err, deploy.ErrAlreadyStopped):
		return httpx.Err(http.StatusConflict, "already_stopped", err.Error())
	case errors.Is(err, deploy.ErrNotStopped):
		return httpx.Err(http.StatusConflict, "not_stopped", err.Error())
	case errors.Is(err, deploy.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "hook_disabled", err.Error())
	case errors.Is(err, deploy.ErrBadSignature):
		return httpx.Err(http.StatusUnauthorized, "bad_signature", err.Error())
	case errors.Is(err, deploy.ErrEnvironmentNotFound):
		return httpx.Err(http.StatusNotFound, "environment_not_found", err.Error())
	case errors.Is(err, deploy.ErrRunNotFound):
		return httpx.Err(http.StatusNotFound, "run_not_found", err.Error())
	case errors.Is(err, deploy.ErrIdempotencyConflict):
		return httpx.Err(http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, deploy.ErrRevisionConflict):
		return httpx.Err(http.StatusConflict, "revision_conflict", err.Error())
	case errors.Is(err, deploy.ErrRunTerminal):
		return httpx.Err(http.StatusConflict, "run_terminal", err.Error())
	case errors.Is(err, deploy.ErrRunNotCancellable):
		return httpx.Err(http.StatusConflict, "run_not_cancellable", err.Error())
	case errors.Is(err, deploy.ErrRunNotRetryable):
		return httpx.Err(http.StatusConflict, "run_not_retryable", err.Error())
	case errors.Is(err, deploy.ErrNoRollbackWatch):
		return httpx.Err(http.StatusConflict, "no_warm_release", err.Error())
	case errors.Is(err, deploy.ErrInvalidPlan), errors.Is(err, deploy.ErrArtifactMissing):
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_release", err.Error())
	case errors.Is(err, deploy.ErrNameTaken):
		return httpx.Err(http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, deploy.ErrProjectArchived):
		return httpx.Err(http.StatusConflict, "project_archived", err.Error())
	case errors.Is(err, deploy.ErrRefNotApplicable):
		return httpx.Err(http.StatusBadRequest, "ref_not_applicable", err.Error())
	case errors.Is(err, deploy.ErrRefNotFound):
		return httpx.Err(http.StatusBadRequest, "ref_not_found", err.Error())
	case errors.As(err, new(*deploy.SourceFailure)):
		// The remote answered and git's output named why: a refused
		// credential, a missing repository, an unreachable host or a commit
		// that is gone. Each has its own remedy, so each keeps its own code.
		return httpx.Err(http.StatusBadGateway, sourceFailureCode(err), err.Error())
	case errors.Is(err, deploy.ErrSourceUnavailable):
		return httpx.Err(http.StatusBadGateway, "source_unavailable", err.Error())
	case errors.Is(err, deploy.ErrPreviewAddressExhausted):
		return httpx.Err(http.StatusConflict, "preview_address_exhausted", err.Error())
	case errors.Is(err, deploy.ErrPreviewAddressMissing):
		return httpx.Err(http.StatusConflict, "preview_address_missing", err.Error())
	case errors.Is(err, deploy.ErrPullRequestRateLimited):
		return httpx.Err(http.StatusTooManyRequests, "github_rate_limited", err.Error())
	case errors.Is(err, deploy.ErrPullRequestUnreadable):
		return httpx.Err(http.StatusBadGateway, "pull_request_unreadable", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func sourceFailureCode(err error) string {
	var failure *deploy.SourceFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return "source_unavailable"
}

func (s *Server) enrichProject(r *http.Request, p *deploy.Project) {
	p.HookURL = "/api/v1/hooks/deploy/" + p.HookID
	if state, err := s.modules.deployer.Inspect(r.Context(), p); err == nil {
		p.CurrentSHA, p.CurrentRef, p.Dirty = state.SHA, state.Ref, state.Dirty
	}
}

func (s *Server) handleDeployList(w http.ResponseWriter, r *http.Request) error {
	// A deploy key narrowed to some deployments lists those and no others,
	// in every view: the rest are not its to know about.
	p := httpx.MustPrincipal(r)
	if r.URL.Query().Get("view") == "fleet" {
		fleet, err := s.modules.deployRuns.Fleet(r.Context(), deploy.QueueBudget{
			Heavy: s.Cfg.DeployHeavySlots,
			Light: s.Cfg.DeployLightSlots,
		})
		if err != nil {
			return httpx.Internal(err)
		}
		fleet.Deployments = slices.DeleteFunc(fleet.Deployments, func(d deploy.DeploymentSummary) bool { return !keyReaches(p, d.ID) })
		fleet.ActiveWork = slices.DeleteFunc(fleet.ActiveWork, func(a deploy.ActiveWork) bool { return !keyReaches(p, a.Run.ProjectID) })
		httpx.JSON(w, http.StatusOK, fleet)
		return nil
	}
	projects, err := s.modules.deployStore.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	projects = slices.DeleteFunc(projects, func(project *deploy.Project) bool { return !keyReaches(p, project.ID) })
	if r.URL.Query().Get("view") == "archived" {
		facts, err := s.modules.deployRuns.ArchivedDeploymentFacts(r.Context())
		if err != nil {
			return httpx.Internal(err)
		}
		archived := make([]archivedDeployment, 0)
		for _, p := range projects {
			if p.ArchivedAt != nil {
				archived = append(archived, archivedDeployment{Project: p, DeploymentFacts: facts[p.ID]})
			}
		}
		httpx.JSON(w, http.StatusOK, archived)
		return nil
	}
	for _, p := range projects {
		s.enrichProject(r, p)
	}
	httpx.JSON(w, http.StatusOK, projects)
	return nil
}

func (s *Server) handleDeployGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	p, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, p)
	if last, err := s.modules.deployStore.LastRun(r.Context(), id); err == nil {
		p.LastRun = last
	}
	running, err := s.modules.deployRuns.ProjectActive(r.Context(), id)
	if err != nil {
		return httpx.Internal(err)
	}
	environmentID, err := environmentQuery(r)
	if err != nil {
		return err
	}
	summary, err := s.modules.deployRuns.EnvironmentDeploymentSummary(r.Context(), id, environmentID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots,
		Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return mapDeployError(err)
	}
	var runtimeOwner deploy.RuntimeObserver
	if s.modules.docker != nil {
		runtimeOwner = s.modules.docker
	}
	runtime := deploy.ObserveRuntimeServices(r.Context(), runtimeOwner, summary.EnvironmentID, summary.LiveReleaseID)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"project": p, "running": running, "deployment": summary, "runtime": runtime,
	})
	return nil
}

type deployProjectRequest struct {
	Name        string `json:"name"`
	RepoPath    string `json:"repoPath"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"composeFile"`
	PreCommand  string `json:"preCommand"`
	PostCommand string `json:"postCommand"`
	Enabled     bool   `json:"enabled"`
}

func (req *deployProjectRequest) toProject() *deploy.Project {
	return &deploy.Project{
		Name: req.Name, RepoPath: req.RepoPath, Branch: req.Branch,
		ComposeFile: req.ComposeFile, PreCommand: req.PreCommand,
		PostCommand: req.PostCommand, Enabled: req.Enabled,
	}
}

func (s *Server) handleDeployCreate(w http.ResponseWriter, r *http.Request) error {
	var req deployProjectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	project, secret, err := s.modules.deployStore.Create(r.Context(), req.toProject())
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, project)
	httpx.SetAudit(r, "deploy.project.create", project.Name,
		map[string]any{"repoPath": project.RepoPath, "branch": project.Branch})
	// The webhook secret is shown exactly once; only its sealed form is kept.
	httpx.JSON(w, http.StatusCreated, map[string]any{"project": project, "secret": secret})
	return nil
}

// deployProjectUpdateRequest distinguishes an omitted field from an explicit
// zero value, which deployProjectRequest cannot: every field there decodes to
// "" or false when the client leaves it out. PUT /deploy/{id} needs that
// distinction so a `{"name": "..."}` body renames a normalized project
// without blanking the legacy fields a full legacy body replaces.
type deployProjectUpdateRequest struct {
	Name        *string `json:"name"`
	RepoPath    *string `json:"repoPath"`
	Branch      *string `json:"branch"`
	ComposeFile *string `json:"composeFile"`
	PreCommand  *string `json:"preCommand"`
	PostCommand *string `json:"postCommand"`
	Enabled     *bool   `json:"enabled"`
}

// renameOnly reports whether the body named only "name", which is the one
// partial shape this route accepts. Anything else — full or partial in some
// other combination — goes through the legacy full-replace path, unchanged.
func (req *deployProjectUpdateRequest) renameOnly() (string, bool) {
	if req.Name == nil || req.RepoPath != nil || req.Branch != nil || req.ComposeFile != nil ||
		req.PreCommand != nil || req.PostCommand != nil || req.Enabled != nil {
		return "", false
	}
	return *req.Name, true
}

// toProject reproduces deployProjectRequest.toProject's zero-value defaulting
// for every field the body left out, so a full legacy body behaves exactly as
// it did before this type existed.
func (req *deployProjectUpdateRequest) toProject() *deploy.Project {
	p := &deploy.Project{}
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.RepoPath != nil {
		p.RepoPath = *req.RepoPath
	}
	if req.Branch != nil {
		p.Branch = *req.Branch
	}
	if req.ComposeFile != nil {
		p.ComposeFile = *req.ComposeFile
	}
	if req.PreCommand != nil {
		p.PreCommand = *req.PreCommand
	}
	if req.PostCommand != nil {
		p.PostCommand = *req.PostCommand
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	return p
}

func (s *Server) handleDeployUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req deployProjectUpdateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if name, ok := req.renameOnly(); ok {
		project, err := s.modules.deployStore.Rename(r.Context(), id, name)
		if err != nil {
			return mapDeployError(err)
		}
		s.enrichProject(r, project)
		httpx.SetAudit(r, "deploy.project.update", project.Name, req)
		httpx.JSON(w, http.StatusOK, project)
		return nil
	}
	project, err := s.modules.deployStore.Update(r.Context(), id, req.toProject())
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, project)
	httpx.SetAudit(r, "deploy.project.update", project.Name, req)
	httpx.JSON(w, http.StatusOK, project)
	return nil
}

func (s *Server) handleDeployDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Archive(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	// The legacy DELETE spelling remains compatible, but its 0.6.7 semantics
	// are the same safe archive as POST /archive. Runtime and data are removed
	// only by the separately previewed remove-managed operation.
	httpx.SetAudit(r, "deploy.archive", project.Name, map[string]any{
		"deploymentId": id, "legacyMethod": true, "resourcesRemoved": false, "schedulesDisabled": true,
	})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDeployRotateSecret(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	secret, err := s.modules.deployStore.RotateSecret(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.project.rotate_secret", project.Name, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"secret": secret})
	return nil
}

func (s *Server) handleDeployRun(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	method, err := s.modules.deployRuns.DeploymentBuildMethod(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	var run *deploy.EngineRun
	if method == deploy.BuildLegacyCompose {
		run, err = s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationDeploy,
			deploy.TriggerManual, p.Username(), "", "")
	} else {
		environmentID, _, environmentErr := s.modules.deployRuns.ProductionEnvironment(r.Context(), id)
		if environmentErr != nil {
			return mapDeployError(environmentErr)
		}
		run, err = s.enqueueNormalizedDeployment(r.Context(), project, environmentID,
			deploy.OperationDeploy, 0, deploy.TriggerManual, p.Username(), "")
	}
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run", project.Name, map[string]any{"branch": project.Branch})
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"started": true, "project": project.Name, "runId": run.ID, "state": run.State,
	})
	return nil
}

type rollbackRequest struct {
	Commit string `json:"commit"`
}

func (s *Server) handleDeployRollback(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rollbackRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := deploy.ValidateSHA(req.Commit); err != nil {
		return httpx.BadRequest("%v", err)
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	// No typed phrase: a rollback is itself the recovery action, reached under
	// exactly the pressure that makes a typing exercise harmful, and it is
	// undone by deploying forward again.
	p := httpx.MustPrincipal(r)
	run, err := s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationRollback,
		deploy.TriggerRollback, p.Username(), req.Commit, "")
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.rollback", project.Name, map[string]any{"commit": req.Commit})
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"started": true, "commit": req.Commit, "runId": run.ID, "state": run.State,
	})
	return nil
}

func (s *Server) handleDeployRuns(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 30)
	if r.URL.Query().Get("view") == "engine" {
		query := r.URL.Query()
		filter := deploy.RunListFilter{Limit: limit}
		if raw := query.Get("environment"); raw != "" {
			filter.Environment, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || filter.Environment <= 0 {
				return httpx.BadRequest("environment must be a positive integer")
			}
		}
		switch query.Get("scope") {
		case "":
		case "production":
			filter.LeaveOutStaging = true
		default:
			return httpx.BadRequest("scope must be \"production\"")
		}
		if raw := query.Get("operation"); raw != "" {
			if !deploy.ValidOperation(deploy.Operation(raw)) {
				return httpx.BadRequest("operation must be a supported deployment action")
			}
			filter.Operation = deploy.Operation(raw)
		}
		if raw := query.Get("state"); raw != "" {
			if raw != "terminal" && raw != "active" && !deploy.ValidRunState(deploy.RunState(raw)) {
				return httpx.BadRequest("state must be a run state, \"terminal\" or \"active\"")
			}
			filter.State = raw
		}
		if raw := query.Get("before"); raw != "" {
			filter.Before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || filter.Before <= 0 {
				return httpx.BadRequest("before must be a positive run id")
			}
		}
		runs, nextBefore, err := s.modules.deployRuns.ProjectRunsFiltered(r.Context(), id, filter)
		if err != nil {
			return httpx.Internal(err)
		}
		running, activeErr := s.modules.deployRuns.ProjectActive(r.Context(), id)
		if activeErr != nil {
			return httpx.Internal(activeErr)
		}
		body := map[string]any{"runs": runs, "running": running}
		if nextBefore != 0 {
			body["nextBefore"] = nextBefore
		}
		httpx.JSON(w, http.StatusOK, body)
		return nil
	}
	runs, err := s.modules.deployStore.Runs(r.Context(), id, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	running, activeErr := s.modules.deployRuns.ProjectActive(r.Context(), id)
	if activeErr != nil {
		return httpx.Internal(activeErr)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"runs":    runs,
		"running": running,
	})
	return nil
}

type deploymentRunCreateRequest struct {
	Operation deploy.Operation `json:"operation"`
	ReleaseID int64            `json:"releaseId,omitempty"`
	// SourceRevision and Ref target a manual deploy or force_build at a
	// specific Git commit or branch/tag, instead of the environment's
	// configured branch head. At most one may be set, and both are refused
	// for anything but a normalized deploy from a remote Git source.
	SourceRevision string `json:"sourceRevision,omitempty"`
	Ref            string `json:"ref,omitempty"`
}

type deploymentRollbackRequest struct {
	ReleaseID int64 `json:"releaseId"`
}

func (s *Server) handleDeploymentReleases(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	releases, err := s.modules.deployRuns.EnvironmentReleases(
		r.Context(), projectID, environmentID, atoiDefault(r.URL.Query().Get("limit"), 30),
	)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, releases)
	return nil
}

func (s *Server) handleDeploymentReleaseRollback(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	var req deploymentRollbackRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ReleaseID <= 0 {
		return httpx.BadRequest("releaseId must be a positive integer")
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	run, err := s.enqueueNormalizedDeployment(r.Context(), project, environmentID,
		deploy.OperationRollback, req.ReleaseID, deploy.TriggerRollback, p.Username(),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.release.rollback", fmt.Sprint(req.ReleaseID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "runId": run.ID,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	var req deploymentRunCreateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Operation == "" {
		req.Operation = deploy.OperationDeploy
	}
	// Preview and schedule lifecycle operations are owned by their own
	// routes and observers. Accepting them here let a service.control
	// principal run the release path under a lifecycle label.
	switch req.Operation {
	case deploy.OperationDeploy, deploy.OperationRedeploy, deploy.OperationRestart, deploy.OperationForceBuild,
		deploy.OperationStop, deploy.OperationStart:
	default:
		return httpx.BadRequest("operation %q cannot be requested manually", req.Operation)
	}
	p := httpx.MustPrincipal(r)
	// Stop and restart interrupt the live runtime exactly like
	// POST /docker/containers/{id}/stop|restart, so they carry the same
	// destructive capability and shared rate budget on top of the
	// service.control this whole route otherwise requires; deploy, redeploy,
	// force_build and start are unaffected. The route can't sit behind
	// s.destructive wholesale because the answer depends on the request body,
	// so the handler checks by hand and fails closed — the same shape
	// handleDBQuery uses once it has classified a statement as destructive.
	if req.Operation == deploy.OperationStop || req.Operation == deploy.OperationRestart {
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"your role does not permit this action ("+string(auth.CapDestructive)+")")
		}
		if !s.destrLim.Allow(p.Username()) {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many destructive actions; try again shortly")
		}
	}
	if req.SourceRevision != "" && req.Ref != "" {
		return httpx.BadRequest("sourceRevision and ref cannot both be set")
	}
	if (req.SourceRevision != "" || req.Ref != "") &&
		req.Operation != deploy.OperationDeploy && req.Operation != deploy.OperationForceBuild {
		return httpx.BadRequest("sourceRevision and ref are accepted only by deploy and force_build")
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	// Preview environments have their own create/update/remove lifecycle;
	// stopping or starting their runtime by hand does not fit it.
	if (req.Operation == deploy.OperationStop || req.Operation == deploy.OperationStart) && target.Kind == deploy.EnvironmentPreview {
		return httpx.BadRequest("operation %q is not available for a preview environment", req.Operation)
	}
	var run *deploy.EngineRun
	if target.BuildMethod == deploy.BuildLegacyCompose {
		if req.Operation != deploy.OperationDeploy || req.ReleaseID != 0 || req.SourceRevision != "" || req.Ref != "" {
			return httpx.BadRequest("operation %q is not available for a legacy deployment", req.Operation)
		}
		productionID, _, productionErr := s.modules.deployRuns.ProductionEnvironment(r.Context(), projectID)
		if productionErr != nil || productionID != environmentID {
			return mapDeployError(deploy.ErrEnvironmentNotFound)
		}
		run, err = s.enqueueLegacyDeployment(r.Context(), project, req.Operation,
			deploy.TriggerManual, p.Username(), "", strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	} else {
		if req.Operation == deploy.OperationRollback {
			return httpx.BadRequest("use the rollback endpoint for a release rollback")
		}
		if req.ReleaseID != 0 {
			return httpx.BadRequest("releaseId is accepted only by rollback")
		}
		run, err = s.enqueueNormalizedDeploymentAtSource(r.Context(), project, environmentID,
			req.Operation, 0, deploy.TriggerManual, p.Username(), strings.TrimSpace(r.Header.Get("Idempotency-Key")),
			nil, req.SourceRevision, req.Ref, 0)
	}
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run.request", fmt.Sprint(run.ID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "operation": req.Operation,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) enqueueNormalizedDeployment(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	operation deploy.Operation,
	targetReleaseID int64,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
) (*deploy.EngineRun, error) {
	return s.enqueueNormalizedDeploymentWithMetadata(ctx, project, environmentID, operation,
		targetReleaseID, trigger, actor, idempotencyKey, nil)
}

func (s *Server) enqueueNormalizedDeploymentWithMetadata(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	operation deploy.Operation,
	targetReleaseID int64,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
	extraMetadata map[string]any,
) (*deploy.EngineRun, error) {
	return s.enqueueNormalizedDeploymentAtSource(ctx, project, environmentID, operation,
		targetReleaseID, trigger, actor, idempotencyKey, extraMetadata, "", "", 0)
}

func (s *Server) enqueueNormalizedDeploymentAtSource(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	operation deploy.Operation,
	targetReleaseID int64,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
	extraMetadata map[string]any,
	sourceRevision string,
	requestedRef string,
	expectedPlanRevision int,
	expectedGitPolicy ...string,
) (*deploy.EngineRun, error) {
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(ctx, project.ID, environmentID)
	if err != nil {
		return nil, err
	}
	if target.BuildMethod == deploy.BuildLegacyCompose {
		return nil, fmt.Errorf("%w: normalized action requires a normalized deployment", deploy.ErrInvalidPlan)
	}
	if operation == deploy.OperationPreviewRemove && (target.Kind != deploy.EnvironmentPreview || trigger != deploy.TriggerPreview) {
		return nil, fmt.Errorf("%w: preview cleanup requires an authorized preview lifecycle event", deploy.ErrInvalidPlan)
	}
	planRevision := target.DesiredRevision
	if expectedPlanRevision != 0 && planRevision != expectedPlanRevision {
		return nil, deploy.ErrRevisionConflict
	}
	variableSnapshotRunID := int64(0)
	var selected *deploy.ReleaseWithArtifacts
	switch operation {
	case deploy.OperationDeploy, deploy.OperationForceBuild, deploy.OperationPreviewCreate, deploy.OperationPreviewUpdate, deploy.OperationPreviewRemove, deploy.OperationScheduled:
		if targetReleaseID != 0 {
			return nil, fmt.Errorf("%w: this operation does not accept a release target", deploy.ErrInvalidPlan)
		}
	case deploy.OperationRedeploy, deploy.OperationRestart:
		if targetReleaseID == 0 {
			targetReleaseID = target.LiveReleaseID
		}
		if targetReleaseID == 0 || targetReleaseID != target.LiveReleaseID {
			return nil, fmt.Errorf("%w: this operation requires the current live release", deploy.ErrInvalidPlan)
		}
	case deploy.OperationStop, deploy.OperationStart:
		if targetReleaseID == 0 {
			targetReleaseID = target.LiveReleaseID
		}
		if targetReleaseID == 0 || targetReleaseID != target.LiveReleaseID {
			return nil, fmt.Errorf("%w: this operation requires the current live release", deploy.ErrInvalidPlan)
		}
		runtime, runtimeErr := s.modules.deployRuns.RuntimeForRelease(ctx, targetReleaseID)
		if runtimeErr != nil {
			return nil, runtimeErr
		}
		if operation == deploy.OperationStop && runtime.State == "stopped" {
			return nil, deploy.ErrAlreadyStopped
		}
		if operation == deploy.OperationStart && runtime.State != "stopped" {
			var observer deploy.RuntimeObserver
			if s.modules.docker != nil {
				observer = s.modules.docker
			}
			if runtime.State != "live" || !deploy.ReleaseRuntimeDown(ctx, observer, *runtime) {
				return nil, deploy.ErrNotStopped
			}
		}
	case deploy.OperationRollback:
		if targetReleaseID <= 0 || targetReleaseID == target.LiveReleaseID {
			return nil, fmt.Errorf("%w: rollback requires a retained, non-live release", deploy.ErrInvalidPlan)
		}
	default:
		return nil, fmt.Errorf("%w: operation %q is not a supported deployment action", deploy.ErrInvalidPlan, operation)
	}
	if targetReleaseID != 0 {
		selected, err = s.modules.deployRuns.Release(ctx, targetReleaseID)
		if err != nil {
			return nil, err
		}
		if selected.Release.ProjectID != project.ID || selected.Release.EnvironmentID != environmentID {
			return nil, fmt.Errorf("%w: release target belongs to another environment", deploy.ErrInvalidPlan)
		}
		if operation == deploy.OperationRollback && selected.Release.State != "retained" {
			return nil, fmt.Errorf("%w: rollback target is not retained", deploy.ErrInvalidPlan)
		}
		if (operation == deploy.OperationRedeploy || operation == deploy.OperationRestart ||
			operation == deploy.OperationStop || operation == deploy.OperationStart) && selected.Release.State != "live" {
			return nil, fmt.Errorf("%w: operation target is no longer live", deploy.ErrInvalidPlan)
		}
		planRevision = selected.Release.PlanRevision
		variableSnapshotRunID = selected.Release.RunID
	}
	source, err := s.modules.deployRuns.SourceConfiguration(ctx, environmentID, planRevision)
	if err != nil {
		return nil, err
	}
	// A manual sourceRevision/ref override only makes sense against a remote
	// Git source: an image, Compose, blueprint or import project has no ref to
	// resolve, and neither does a local Git checkout (nothing to fetch it
	// from). Every other caller of this function — the git watcher, provider
	// hooks, preview approvals — passes its own pre-resolved sourceRevision
	// under a non-manual trigger, so this refusal never reaches them.
	manualOverride := trigger == deploy.TriggerManual && (sourceRevision != "" || requestedRef != "")
	if manualOverride && !(source.Kind == deploy.SourceGit && deploy.IsRemoteGitSource(source)) {
		return nil, deploy.ErrRefNotApplicable
	}
	// A pull request's head reaches an environment through "Test this pull
	// request", where an administrator approves that exact commit for a
	// preview; named here it would deploy anyone's pull request to production
	// with no approval at all.
	if requestedRef != "" && deploy.IsProviderPullRef(requestedRef) && target.Kind != deploy.EnvironmentPreview {
		return nil, fmt.Errorf("%w: %s is a pull request head, which only a preview environment builds", deploy.ErrRefNotApplicable, requestedRef)
	}
	if deploy.IsRemoteGitSource(source) {
		switch {
		case selected != nil:
			sourceRevision = selected.Release.SourceRevision
		case sourceRevision != "":
			// An explicit object id from the caller — normalizeRunRequest
			// validates its shape; nothing to resolve here.
		case requestedRef != "":
			sourceRevision, err = s.modules.deploySources.ResolveGitRef(ctx, source, requestedRef)
			if err != nil {
				return nil, err
			}
		case operation != deploy.OperationPreviewRemove:
			if target.Kind == deploy.EnvironmentPreview {
				sourceRevision, err = s.modules.deployAutomation.ApprovedPreviewRevision(ctx, environmentID, planRevision)
			} else {
				sourceRevision, err = s.modules.deploySources.ResolveGitRevision(ctx, source)
			}
			if err != nil {
				return nil, err
			}
		}
	} else {
		sourceRevision = ""
	}
	metadataValues := map[string]any{
		"compatibility": false, "targetReleaseId": targetReleaseID, "changedPaths": []string{},
	}
	if manualOverride {
		metadataValues["requestedRevision"] = sourceRevision
		if requestedRef != "" {
			metadataValues["requestedRef"] = requestedRef
		}
	}
	for key, value := range extraMetadata {
		metadataValues[key] = value
	}
	metadata, err := json.Marshal(metadataValues)
	if err != nil {
		return nil, err
	}
	steps := append([]deploy.StepKey(nil), deploy.DefaultStepKeys...)
	slot := deploy.SlotHeavy
	priority := 500
	switch operation {
	case deploy.OperationRedeploy, deploy.OperationRollback:
		steps = []deploy.StepKey{
			deploy.StepRenderRuntime, deploy.StepBackupGate, deploy.StepProvisionCertificate,
			deploy.StepStartCandidate, deploy.StepVerifyReadiness, deploy.StepVerifySmoke,
			deploy.StepActivate, deploy.StepRetirePrevious, deploy.StepRecordRelease,
			deploy.StepNotify,
		}
	case deploy.OperationRestart, deploy.OperationStart:
		// Start verifies readiness/smoke against the live release exactly as
		// restart does, so a start that comes up broken is still reported.
		steps = []deploy.StepKey{
			deploy.StepStartCandidate, deploy.StepVerifyReadiness, deploy.StepVerifySmoke,
			deploy.StepRecordRelease, deploy.StepNotify,
		}
		slot = deploy.SlotLight
	case deploy.OperationStop:
		steps = []deploy.StepKey{deploy.StepStartCandidate, deploy.StepRecordRelease, deploy.StepNotify}
		slot = deploy.SlotLight
	case deploy.OperationPreviewRemove:
		steps = []deploy.StepKey{deploy.StepRetirePrevious, deploy.StepNotify}
		slot = deploy.SlotLight
	}
	if marker, _ := extraMetadata["scheduleMarker"].(bool); marker {
		steps = []deploy.StepKey{deploy.StepNotify}
		slot = deploy.SlotLight
	}
	// An instant rollback builds and starts nothing: it resumes the warm
	// release and switches back to it, so it takes a light slot.
	instant, _ := extraMetadata["instant"].(bool)
	if instant && operation == deploy.OperationRollback {
		steps = append([]deploy.StepKey(nil), deploy.InstantRollbackSteps...)
		slot = deploy.SlotLight
	}
	if operation == deploy.OperationRollback {
		priority = 1000
	}
	digestInput := fmt.Sprintf("normalized:%d:%d:%s:%d:%d", project.ID, environmentID,
		operation, planRevision, targetReleaseID)
	if instant {
		digestInput += fmt.Sprintf(":instant:%v", extraMetadata["watchId"])
	}
	digest := sha256.Sum256([]byte(digestInput))
	policyKey := ""
	if len(expectedGitPolicy) > 0 {
		policyKey = expectedGitPolicy[0]
	}
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID, Operation: operation,
		Trigger: trigger, Actor: actor, IdempotencyKey: idempotencyKey,
		RequestDigest: hex.EncodeToString(digest[:]), PlanRevision: planRevision,
		SourceRevision: sourceRevision, ExpectedPlanRevision: expectedPlanRevision,
		ExpectedGitPolicy:     policyKey,
		VariableSnapshotRunID: variableSnapshotRunID,
		Priority:              priority, SlotClass: slot, Metadata: metadata, Steps: steps,
	})
	if err != nil {
		return nil, err
	}
	s.modules.deployEngine.Notify()
	return run, nil
}

// handleDeploymentInsights answers the delivery figures for one project over
// a window of days (7..365, default 30), computed from persisted runs only.
func (s *Server) handleDeploymentInsights(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployStore.Get(r.Context(), projectID); err != nil {
		return mapDeployError(err)
	}
	environmentID, err := environmentQuery(r)
	if err != nil {
		return err
	}
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	var insights *deploy.DeploymentInsights
	if environmentID > 0 {
		insights, err = s.modules.deployRuns.EnvironmentInsights(r.Context(), projectID, environmentID, days)
	} else {
		insights, err = s.modules.deployRuns.ProjectInsights(r.Context(), projectID, days)
	}
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, insights)
	return nil
}

func (s *Server) handleDeploymentRunGet(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	httpx.JSON(w, http.StatusOK, snapshot)
	return nil
}

// handleDeploymentRunSettingsDrift says what changed in the environment's
// settings since a run was planned, so a failed run's page can offer a deploy
// with the current settings rather than a retry of the ones that failed. It
// reads what the configuration read already shows: plan fields, and variable
// names and scopes without values.
func (s *Server) handleDeploymentRunSettingsDrift(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	drift, err := s.modules.deployPlanning.RunSettingsDrift(r.Context(), projectID, runID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, drift)
	return nil
}

func (s *Server) handleDeploymentRunMetrics(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	var owner deploy.MetricsObserver
	if s.modules.metrics != nil {
		owner = s.modules.metrics
	}
	result, err := s.modules.deployRuns.RunMetrics(r.Context(), owner, *snapshot)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleDeploymentRunLogs(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	var owner deploy.RuntimeObserver
	if s.modules.docker != nil {
		owner = s.modules.docker
	}
	httpx.JSON(w, http.StatusOK, deploy.ObserveRunLogs(r.Context(), owner, *snapshot))
	return nil
}

func (s *Server) handleDeploymentRunCancel(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	run, err := s.modules.deployRuns.Run(r.Context(), runID)
	if err != nil || run.ProjectID != projectID {
		if err == nil {
			err = deploy.ErrRunNotFound
		}
		return mapDeployError(err)
	}
	run, err = s.modules.deployEngine.Cancel(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run.cancel", fmt.Sprint(runID), nil)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunRetry(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	prior, err := s.modules.deployRuns.Run(r.Context(), runID)
	if err != nil || prior.ProjectID != projectID {
		if err == nil {
			err = deploy.ErrRunNotFound
		}
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	run, _, err := s.modules.deployRuns.Retry(r.Context(), runID, p.Username(),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return mapDeployError(err)
	}
	s.modules.deployEngine.Notify()
	httpx.SetAudit(r, "deploy.run.retry", fmt.Sprint(run.ID), map[string]any{"retryOfRunId": runID})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunStream(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			return httpx.BadRequest("after must be a non-negative sequence")
		}
	}
	backlog, live, unsubscribe, err := s.modules.deployRuns.Subscribe(r.Context(), runID, after)
	if err != nil {
		return mapDeployError(err)
	}
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	s.recordAudit(r, "deploy.run.stream.open", fmt.Sprint(runID), map[string]any{"after": after})
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)
	if err := conn.Send("snapshot", snapshot); err != nil {
		return nil
	}
	for {
		if len(backlog) > 0 {
			if err := conn.Send("events", backlog); err != nil {
				return nil
			}
			after = backlog[len(backlog)-1].Seq
		}
		if len(backlog) < deploy.RunEventReplayLimit {
			break
		}
		// A full page is not the end of retained history. Re-subscribe after
		// the delivered cursor so events committed during replay are read from
		// the store; only the final page hands off to its live subscription.
		unsubscribe()
		backlog, live, unsubscribe, err = s.modules.deployRuns.Subscribe(ctx, runID, after)
		if err != nil {
			conn.SendError("could not replay deployment events; reconnect to resume")
			return nil
		}
	}
	batch := make([]deploy.RunEvent, 0, 64)
	flush := time.NewTicker(120 * time.Millisecond)
	defer flush.Stop()
	send := func() bool {
		if len(batch) == 0 {
			return true
		}
		if err := conn.Send("events", batch); err != nil {
			return false
		}
		batch = batch[:0]
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, open := <-live:
			if !open {
				send()
				if final, err := s.modules.deployRuns.Snapshot(context.Background(), runID); err == nil {
					conn.Send("snapshot", final)
				}
				return nil
			}
			batch = append(batch, event)
			if len(batch) == cap(batch) && !send() {
				return nil
			}
		case <-flush.C:
			if !send() {
				return nil
			}
		}
	}
}

func deploymentRunIDs(r *http.Request) (projectID, runID int64, err error) {
	projectID, err = parseID(r)
	if err != nil {
		return 0, 0, err
	}
	runID, parseErr := strconv.ParseInt(chi.URLParam(r, "run"), 10, 64)
	if parseErr != nil || runID <= 0 {
		return 0, 0, httpx.BadRequest("run id must be a positive integer")
	}
	return projectID, runID, nil
}

func (s *Server) handleDeployCommits(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	commits, err := s.modules.deployer.History(r.Context(), project,
		atoiDefault(r.URL.Query().Get("limit"), 30))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, commits)
	return nil
}

func (s *Server) handleDeployEnvList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	vars, err := s.modules.deployStore.ListEnv(r.Context(), id, false)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, vars)
	return nil
}

// handleDeployEnvReveal returns the plaintext values. It is a separate,
// admin-only, audited endpoint precisely so that reading a project's secrets
// leaves a trail naming who read them and when.
func (s *Server) handleDeployEnvReveal(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	vars, err := s.modules.deployStore.ListEnv(r.Context(), id, true)
	if err != nil {
		return httpx.Internal(err)
	}
	p := httpx.MustPrincipal(r)
	s.Audit.Record(r.Context(), audit.Entry{
		UserID: p.UserID(), Username: p.Username(), Role: string(p.Role),
		IP: httpx.ClientIP(r), Actor: p.Kind, KeyID: p.KeyID(), KeyName: p.KeyName(),
		Action: "deploy.env.reveal", Target: project.Name,
		Method: r.Method, Path: r.URL.Path, Status: http.StatusOK, Success: true,
		Detail: audit.Detail(map[string]any{"count": len(vars)}),
	})
	httpx.JSON(w, http.StatusOK, vars)
	return nil
}

type envSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Server) handleDeployEnvSet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req envSetRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	if err := s.modules.deployStore.SetEnv(r.Context(), id, req.Key, req.Value); err != nil {
		return mapDeployError(err)
	}
	// Only the variable name is recorded — auditing the value would defeat
	// the point of sealing it.
	httpx.SetAudit(r, "deploy.env.set", project.Name, map[string]any{"key": req.Key})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDeployEnvDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	key := chi.URLParam(r, "key")
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	if err := s.modules.deployStore.DeleteEnv(r.Context(), id, key); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.env.delete", project.Name, map[string]any{"key": key})
	httpx.NoContent(w)
	return nil
}

// maxHookBody bounds what a webhook may post. Provider payloads are tens of
// kilobytes; anything larger is not a push event.
const maxHookBody = 1 << 20

// handleDeployWebhook is the CI entry point. It is the only route reached
// without a dashboard session, so it authenticates by HMAC over the raw body
// with the project's own secret — and it still sits behind the network
// allowlist that fronts the whole API.
func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) error {
	hookID := chi.URLParam(r, "hookID")
	httpx.SetAuditActor(r, "webhook")
	project, err := s.modules.deployStore.ByHookID(r.Context(), hookID)
	if err != nil {
		// A wrong hook id and a wrong signature are both reported as
		// unauthorized so the endpoint does not confirm which ids exist. The
		// attempted id goes into the audit trail, though: a run of these is
		// what an enumeration scan looks like from the inside.
		httpx.SetAudit(r, "deploy.webhook", hookID, map[string]any{"reason": "unknown hook id"})
		return httpx.Err(http.StatusUnauthorized, "unauthorized", "unknown or unauthorised deploy hook")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		return httpx.BadRequest("could not read request body")
	}
	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		signature = r.Header.Get("X-Signature-256")
	}
	if err := s.modules.deployStore.VerifySignature(r.Context(), project.ID, body, signature); err != nil {
		httpx.SetAudit(r, "deploy.webhook", project.Name, map[string]any{"reason": "signature mismatch"})
		return httpx.Err(http.StatusUnauthorized, "unauthorized", "unknown or unauthorised deploy hook")
	}
	if !project.Enabled {
		httpx.SetAudit(r, "deploy.webhook", project.Name, map[string]any{"reason": "hook disabled"})
		return httpx.Err(http.StatusForbidden, "hook_disabled", deploy.ErrDisabled.Error())
	}
	httpx.SetAudit(r, "deploy.webhook", project.Name,
		map[string]any{"branch": project.Branch, "bytes": len(body)})
	run, err := s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationDeploy,
		deploy.TriggerLegacyHook, "ci", "", "")
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"accepted": true, "project": project.Name, "branch": project.Branch,
		"runId": run.ID, "state": run.State,
	})
	return nil
}

func (s *Server) enqueueLegacyDeployment(
	ctx context.Context,
	project *deploy.Project,
	operation deploy.Operation,
	trigger deploy.TriggerKind,
	actor, targetCommit, idempotencyKey string,
) (*deploy.EngineRun, error) {
	environmentID, revision, err := s.modules.deployRuns.ProductionEnvironment(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]any{
		"compatibility": true, "targetCommit": targetCommit, "changedPaths": []string{},
	})
	if err != nil {
		return nil, err
	}
	digestInput := fmt.Sprintf("legacy:%d:%d:%s:%s:%s:%s",
		project.ID, environmentID, operation, trigger, project.Branch, targetCommit)
	digest := sha256.Sum256([]byte(digestInput))
	priority := 500
	if operation == deploy.OperationRollback {
		priority = 1000
	}
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID,
		Operation: operation, Trigger: trigger, Actor: actor,
		IdempotencyKey: idempotencyKey,
		RequestDigest:  hex.EncodeToString(digest[:]), PlanRevision: revision,
		Priority: priority, SlotClass: deploy.SlotHeavy,
		Metadata: metadata, Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
	})
	if err != nil {
		return nil, err
	}
	s.modules.deployEngine.Notify()
	return run, nil
}

func (s *Server) handleDeployPurge(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	current, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	if err := httpx.RequireTypedConfirmation(w, r, current.Name); err != nil {
		return err
	}
	project, err := s.modules.deployStore.PurgeArchived(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.project.purge", project.Name, map[string]any{"deploymentId": id, "resourcesRemoved": false})
	httpx.NoContent(w)
	return nil
}

// Each row carries what it deployed beside the project record, so the
// archived list can draw it as that product without a read per row.
type archivedDeployment struct {
	*deploy.Project
	deploy.DeploymentFacts
}
