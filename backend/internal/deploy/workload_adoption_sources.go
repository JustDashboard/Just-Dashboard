package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"gopkg.in/yaml.v3"
)

// WithRecoveryRoot configures the server-owned snapshot store. It does not add
// that store to the paths clients may supply for ordinary local sources.
func (a *HostSourceAnalyzer) WithRecoveryRoot(root string) *HostSourceAnalyzer {
	a.recoveryRoot = root
	return a
}

func stageRecoveredSource(ctx context.Context, recoveryRoot string, populate func(string) error) (string, string, error) {
	if !filepath.IsAbs(recoveryRoot) {
		return "", "", ErrInvalidSource
	}
	store := filepath.Join(recoveryRoot, "sources")
	if err := makePrivateDirectory(store); err != nil {
		return "", "", err
	}
	staging, err := os.MkdirTemp(store, "capture-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(staging)
	tree := filepath.Join(staging, "tree")
	if err := os.Mkdir(tree, 0700); err != nil {
		return "", "", err
	}
	if err := populate(tree); err != nil {
		return "", "", err
	}
	digest, err := localDirectoryDigest(ctx, tree)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(store, strings.TrimPrefix(digest, "sha256:"))
	if err := os.Rename(staging, target); err != nil {
		// Concurrent captures with the same content may share the immutable
		// artifact only after its full tree is verified.
		actual, verifyErr := localDirectoryDigest(ctx, filepath.Join(target, "tree"))
		if verifyErr != nil || actual != digest {
			return "", "", err
		}
	}
	return digest, filepath.Join(target, "tree"), nil
}

func (a *HostSourceAnalyzer) recoveredSnapshotRoot(ctx context.Context, handle string) (string, error) {
	if !contentDigestRE.MatchString(handle) || !filepath.IsAbs(a.recoveryRoot) {
		return "", ErrInvalidSource
	}
	root := filepath.Join(a.recoveryRoot, "sources", strings.TrimPrefix(handle, "sha256:"), "tree")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", ErrSourceUnavailable
	}
	digest, err := localDirectoryDigest(ctx, root)
	if err != nil || digest != handle {
		return "", fmt.Errorf("%w: recovered source snapshot changed or is unavailable", ErrSourceUnavailable)
	}
	return root, nil
}

func (a *HostSourceAnalyzer) analyzeRecoveredSnapshot(ctx context.Context, source DraftSourceConfig) (DetectionResult, error) {
	root, err := a.recoveredSnapshotRoot(ctx, source.ResourceID)
	if err != nil {
		return DetectionResult{}, err
	}
	if source.Kind == SourceCompose {
		return a.analyzeCompose(ctx, source, source.ComposeFiles, root)
	}
	selected, err := detectionSubdirectory(root, source.Subdirectory)
	if err != nil {
		return DetectionResult{}, err
	}
	return a.detector.DetectPath(ctx, selected, SourceIdentity{Kind: SourceLocal, Digest: source.ResourceID})
}

func writeRecoveredComposeDocuments(root string, documents []ComposeDocument) error {
	for _, document := range documents {
		if !safeRelativePath(document.Path) {
			return ErrInvalidCompose
		}
		path := filepath.Join(root, filepath.FromSlash(document.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return ErrInvalidCompose
		}
		if err := os.WriteFile(path, []byte(document.Content), 0600); err != nil {
			return err
		}
	}
	return nil
}

func (r *dockerRecovery) recoveredPrimaryService(analysis ComposeAnalysis) string {
	services := append([]ComposeServicePlan(nil), analysis.Services...)
	for i := range services {
		if captures := r.containers[services[i].Name]; len(captures) > 0 && captures[0].Inspection.Config != nil {
			services[i].Image = captures[0].Inspection.Config.Image
		}
		if _, exists := r.builds[services[i].Name]; exists {
			services[i].BuildContext = "."
		}
	}
	return composePrimaryService(services)
}

func (r *dockerRecovery) attachBuildSources(ctx context.Context, recoveryRoot string) error {
	var model map[string]any
	if yaml.Unmarshal([]byte(r.result.Source.ComposeFiles[0].Content), &model) != nil {
		return ErrInvalidCompose
	}
	services := object(model["services"])
	attached := false
	type preparedContext struct {
		source   string
		build    map[string]any
		relative string
	}
	contexts := map[string]preparedContext{}
	for _, service := range sortedObjectKeys(services) {
		entry := RecoveredBuildSource{Service: service, Status: "image_only", Reason: "No verified local build source was retained; deployments use the captured immutable image."}
		if captures := r.containers[service]; len(captures) > 0 {
			capturedBuildSourceEvidence(&entry, captures[0])
		}
		original := r.builds[service]
		if original == nil {
			r.result.Adoption.BuildSources = append(r.result.Adoption.BuildSources, entry)
			continue
		}
		build := object(original)
		if value, ok := original.(string); ok {
			build = map[string]any{"context": value}
		}
		contextPath, _ := build["context"].(string)
		if contextPath == "" {
			contextPath = "."
		}
		resolved, resolveErr := r.resolveOriginalReference(r.result.Adoption.OriginalSourcePath, contextPath, false)
		dockerfile, _ := build["dockerfile"].(string)
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		if resolveErr == nil && filepath.IsAbs(dockerfile) {
			dockerfile, resolveErr = filepath.Rel(resolved, dockerfile)
		}
		if resolveErr != nil || !safeRelativePath(dockerfile) || build["dockerfile_inline"] != nil || build["additional_contexts"] != nil || build["secrets"] != nil || build["ssh"] != nil {
			entry.Reason = "The original build needs unavailable, external or private inputs. Its immutable live image remains deployable."
			r.result.Adoption.BuildSources = append(r.result.Adoption.BuildSources, entry)
			continue
		}
		if !recoverableBuildArguments(build) {
			entry.Reason = "The original build arguments need unresolved environment or private inputs. The pinned live image remains deployable."
			r.result.Adoption.BuildSources = append(r.result.Adoption.BuildSources, entry)
			continue
		}
		build["dockerfile"] = filepath.ToSlash(dockerfile)
		relative := filepath.ToSlash(filepath.Join("contexts", service))
		build["context"] = relative
		r.sanitizeStrings(build, service, "build")
		escapeRecoveredLiterals(build, r.result.Environment)
		contexts[service] = preparedContext{resolved, build, relative}
		r.result.Adoption.BuildSources = append(r.result.Adoption.BuildSources, entry)
	}
	if len(contexts) == 0 {
		return nil
	}
	// Validate each bounded context independently. A unavailable context never
	// removes the pinned image fallback of another service.
	store := filepath.Join(recoveryRoot, "source-captures")
	if err := makePrivateDirectory(store); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(store, "build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for i := range r.result.Adoption.BuildSources {
		entry := &r.result.Adoption.BuildSources[i]
		prepared, exists := contexts[entry.Service]
		if !exists {
			continue
		}
		target := filepath.Join(staging, filepath.FromSlash(prepared.relative))
		exclusions := []string{}
		if relative, err := filepath.Rel(prepared.source, recoveryRoot); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, "../") {
			exclusions = append(exclusions, relative)
		}
		before, err := localDirectoryDigest(ctx, prepared.source, exclusions)
		if err == nil {
			err = copyContainedTree(prepared.source, target, copyTreeLimits{ExcludePrivateFiles: true, ExcludePaths: exclusions})
		}
		after, afterErr := localDirectoryDigest(ctx, target)
		if err != nil || afterErr != nil || before != after || !regularExists(target, prepared.build["dockerfile"].(string)) {
			_ = os.RemoveAll(target)
			entry.Reason = "The build source could not be captured within its path, size and private-file limits. The pinned live image is retained."
			continue
		}
		detection, detectErr := (Detector{}).DetectPath(ctx, target, SourceIdentity{Kind: SourceLocal, Digest: after})
		if detectErr == nil {
			if candidate := selectedHostRecoveryCandidate(detection); candidate != nil {
				if candidate.BuildMethod == BuildDockerfile {
					for index := range detection.Candidates {
						alternative := &detection.Candidates[index]
						if alternative.Root == candidate.Root && alternative.BuildMethod == BuildRecipe {
							candidate = alternative
							break
						}
					}
				}
				entry.Name, entry.Framework, entry.Language, entry.Role, entry.Confidence = candidate.Name, candidate.Framework, candidate.Recipe, candidate.Profile, candidate.Confidence
			}
		}
		object(services[entry.Service])["build"] = prepared.build
		entry.Status, entry.Reason, entry.SnapshotDigest = "snapshot", "Verified local build inputs were captured; the live image remains the rollback baseline.", after
		attached = true
	}
	if !attached {
		return nil
	}
	content, err := yaml.Marshal(model)
	if err != nil {
		return err
	}
	documents := []ComposeDocument{{Path: "compose.yml", Content: string(content)}}
	analysis, err := analyzeComposeDocuments(documents)
	if err != nil {
		for i := range r.result.Adoption.BuildSources {
			if r.result.Adoption.BuildSources[i].Status == "snapshot" {
				r.result.Adoption.BuildSources[i].Status = "image_only"
				r.result.Adoption.BuildSources[i].Reason = "The preserved build definition needs an explicit supported recipe. The pinned image remains deployable."
			}
		}
		return nil
	}
	digest, _, err := stageRecoveredSource(ctx, recoveryRoot, func(tree string) error {
		if err := copyContainedTree(staging, tree, copyTreeLimits{ExcludePrivateFiles: true}); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(tree, "compose.yml"), content, 0600)
	})
	if err != nil {
		return err
	}
	analysis.PrimaryService = r.result.Configuration.Build.PrimaryService
	r.result.Source = DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeRecoveredSnapshot, ResourceID: digest, ComposeFiles: documents}
	r.result.Detection.Source.Digest = analysis.Digest
	r.result.Detection.Compose = &analysis
	seen := map[string]bool{}
	for _, variable := range r.result.Configuration.Variables {
		seen[variable.Name] = true
	}
	for _, name := range sortedStringMapKeys(r.result.Environment) {
		if !seen[name] {
			r.result.Configuration.Variables = append(r.result.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"build"}, ValueMode: "literal"})
			AddRecoveredInput(r.result, name, name, "", "runtime_setting", "compose", "runtime_setting")
		}
	}
	return nil
}

func recoverableBuildArguments(build map[string]any) bool {
	if raw, exists := build["args"]; exists {
		arguments, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		for name, raw := range arguments {
			if raw == nil || secretShapedKey(name) {
				return false
			}
			value := fmt.Sprint(raw)
			if containsURLCredentials(value) || browserSecretValue.MatchString(value) || containsSourceInterpolation(value) {
				return false
			}
		}
	}
	return true
}

func capturedBuildSourceEvidence(entry *RecoveredBuildSource, capture *dockerx.AdoptionContainer) {
	config := capture.Inspection.Config
	if config == nil {
		return
	}
	if engine, backing := composeImageFamily(config.Image); backing {
		entry.Framework, entry.Role, entry.Confidence = engine, ProfileService, ConfidenceMedium
		entry.Reason = "The original image identifies a backing service; no verified local source was retained. Its exact image remains deployable."
		return
	}
	command := append(append([]string(nil), config.Entrypoint...), config.Cmd...)
	for _, argument := range command {
		base := filepath.Base(argument)
		switch {
		case base == "node" || base == "nodejs":
			entry.Language = "node"
		case base == "python" || base == "python3" || strings.HasPrefix(base, "python3."):
			entry.Language = "python"
		case base == "next" || strings.Contains(argument, "/node_modules/next/dist/bin/next") || strings.Contains(argument, ".next/standalone/") && strings.HasSuffix(argument, "server.js"):
			entry.Framework, entry.Language, entry.Role = "next", "node", ProfileWeb
		case base == "uvicorn" || base == "gunicorn":
			entry.Language, entry.Role = "python", ProfileWeb
		case base == "celery" || base == "rq":
			entry.Language, entry.Role = "python", ProfileWorker
		}
	}
	if entry.Language != "" || entry.Framework != "" {
		entry.Confidence = ConfidenceMedium
		entry.Reason = "The captured startup command identifies this runtime; framework details beyond that evidence remain unverified. The exact live image is retained without inventing source code."
	}
}
