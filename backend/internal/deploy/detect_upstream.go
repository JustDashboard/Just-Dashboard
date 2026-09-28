package deploy

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// Pasting the upstream repository of a self-hosted application the catalogue
// already packages — n8n, Gitea, Uptime Kuma — built it from source: a large
// monorepo, often past detection's bounds, whose development tree is not how
// the project says to run it. The reviewed template (with its volumes,
// variables and health check), or the image the project publishes itself, is
// the route its own documentation takes, and detection now offers it.

var (
	workflowGHCRRE      = regexp.MustCompile(`ghcr\.io/([A-Za-z0-9._-]+)/([A-Za-z0-9._/-]+)`)
	workflowDockerHubRE = regexp.MustCompile(`docker\.io/([a-z0-9._-]+)/([a-z0-9._/-]+)`)
)

// repositorySlug reads host/owner/name out of a Git URL, lower-cased and
// without a .git suffix, or "" for anything else.
func repositorySlug(raw string) string {
	raw = strings.TrimSpace(raw)
	host, repositoryPath := "", ""
	if match := gitModuleURLHostRE.FindStringSubmatch(raw); match != nil && !strings.Contains(raw, "://") {
		host = match[1]
		repositoryPath = raw[len(match[0]):]
	} else if parsed, err := url.Parse(raw); err == nil && parsed.Hostname() != "" {
		host, repositoryPath = parsed.Hostname(), parsed.Path
	}
	repositoryPath = strings.TrimSuffix(strings.Trim(repositoryPath, "/"), ".git")
	segments := strings.Split(repositoryPath, "/")
	if host == "" || len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return ""
	}
	return strings.ToLower(host + "/" + segments[0] + "/" + segments[1])
}

func (s *repoShapeScan) upstreamAlternatives(identity SourceIdentity) []DetectionAlternative {
	slug := repositorySlug(identity.Remote)
	if slug == "" {
		return nil
	}
	var alternatives []DetectionAlternative
	if catalogue, err := blueprint.Catalog(); err == nil {
		for _, entry := range catalogue {
			if supported, _ := blueprint.DeploymentSupport(entry); !supported || repositorySlug(entry.Provenance.UpstreamURL) != slug {
				continue
			}
			alternatives = append(alternatives, DetectionAlternative{
				Kind: "template", Ref: entry.ID, Label: boundedText(entry.Name, 256),
				Evidence: boundedText(entry.Name+" has a reviewed template built from this repository's published image", 512),
			})
		}
	}
	_, owner, _ := strings.Cut(slug, "/")
	owner, name, _ := strings.Cut(owner, "/")
	images := map[string]string{}
	files := make([]string, 0, len(s.workflows))
	for file := range s.workflows {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		text := string(s.workflows[file])
		if !strings.Contains(text, "docker/build-push-action") && !strings.Contains(text, "docker push") &&
			!strings.Contains(text, "docker/metadata-action") {
			continue
		}
		text = strings.NewReplacer(
			"${{ github.repository }}", owner+"/"+name, "${{github.repository}}", owner+"/"+name,
			"${{ github.repository_owner }}", owner, "${{github.repository_owner}}", owner,
		).Replace(text)
		for _, expression := range []*regexp.Regexp{workflowGHCRRE, workflowDockerHubRE} {
			for _, match := range expression.FindAllStringSubmatch(text, -1) {
				imageOwner, imageName := strings.ToLower(strings.TrimSpace(match[1])), strings.ToLower(strings.TrimSuffix(match[2], "/"))
				if imageOwner != owner {
					continue
				}
				registry := "ghcr.io/"
				if expression == workflowDockerHubRE {
					registry = "docker.io/"
				}
				image := registry + imageOwner + "/" + imageName
				if _, err := normalizeImageReference(image); err == nil && images[image] == "" {
					images[image] = file
				}
			}
		}
	}
	published := make([]string, 0, len(images))
	for image := range images {
		published = append(published, image)
	}
	sort.Strings(published)
	for _, image := range published {
		if len(alternatives) >= 4 {
			break
		}
		alternatives = append(alternatives, DetectionAlternative{
			Kind: "image", Ref: image, Label: image,
			Evidence: boundedText("the project publishes this image from "+images[image], 512),
		})
	}
	return alternatives
}
