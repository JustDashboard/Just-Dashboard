package deploy

import (
	"fmt"
	"strings"
)

// Preflight's reading of a static site: what its generator release, its
// sub-path, its theme and its hosting rules mean for the deployment, said
// before Deploy rather than found by a visitor.

func staticSiteFindings(source *DraftSourceConfig, detection *DetectionResult, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	candidate := plannedDetectionCandidate(detection, build)
	if candidate == nil || candidate.StaticSite == nil {
		return nil
	}
	static := build.Method == BuildStatic || (build.Method == BuildRecipe && strings.TrimSpace(build.OutputDirectory) != "")
	if !static {
		return nil
	}
	site := candidate.StaticSite
	var findings []PreflightFinding
	label := siteGeneratorNames[site.Generator]
	switch {
	case site.BasePath != "":
		findings = append(findings, finding("static_base_path", PreflightWarning,
			"The site is built for the sub-path "+site.BasePath+"/", site.BasePath+" ("+site.BasePathSource+")",
			"Its pages and assets link under "+site.BasePath+"/, as on a GitHub Pages project site; they are served there, and / redirects to it.",
			"Set the base to / in "+site.BasePathSource+" to serve the site at the domain's root; deploying as is works.",
			"deploy", "configuration.build"))
	case site.BasePathExpression:
		findings = append(findings, finding("static_base_path_computed", PreflightWarning,
			"The site's base path is computed while it builds", site.BasePathSource,
			"The files are served at the root. If the expression gives a sub-path on this server, the pages link under it and their assets answer 404.",
			"Write the base as a literal, or make sure it evaluates to / for this deployment.", "deploy", "configuration.build"))
	}
	if build.Recipe == candidate.Recipe && build.Method == BuildRecipe {
		switch {
		case site.Generator == "jekyll" && site.VersionIssue != "":
			findings = append(findings, finding("jekyll_ruby_version", PreflightWarning,
				"The site asks for a Ruby the recipe does not build with", site.VersionIssue,
				"The Jekyll recipe builds on Ruby 3.1 to 3.4; gems locked for another release may fail to install.",
				"Declare Ruby 3.1 to 3.4 in .ruby-version, or build with a Dockerfile on the release the site needs.", "deploy", "configuration.build"))
		case site.VersionIssue != "":
			findings = append(findings, finding("site_generator_version", PreflightWarning,
				"The declared "+label+" release is not the one that builds", site.VersionIssue,
				"Templates and configuration can change between releases, so the site may build differently or not at all.",
				"Pin a release that has an official image, or build with a Dockerfile on the one the site needs.", "deploy", "configuration.build"))
		case site.Generator == "hugo" && site.Unpinned:
			findings = append(findings, finding("hugo_version_unpinned", PreflightWarning,
				"The site does not pin its Hugo release", "builds with Hugo "+site.Version,
				"Hugo removes deprecated template functions between releases, so a site written for an older one can fail to build, and a later rebuild may use a newer one.",
				"Pin the release the site is written for: a .hvm file (v0.139.0), HUGO_VERSION in netlify.toml, or hugo-version in the deploy workflow.",
				"deploy", "configuration.build"))
		}
		if site.ThemeSubmodule != "" && source != nil && !source.IncludeSubmodules {
			findings = append(findings, finding("site_theme_in_submodule", PreflightBlocked,
				"The site's theme is a Git submodule that will not be fetched", site.ThemeSubmodule,
				"The theme directory is empty in the build without it, and "+label+" stops with a missing layout or theme error.",
				"Turn on submodules in the Source section, or commit the theme into the repository.", "git", "source.includeSubmodules"))
		}
	}
	if site.HostingRulesLeftOut > 0 {
		findings = append(findings, finding("static_redirects_unsupported", PreflightWarning,
			"Some hosting rules are not applied", fmt.Sprintf("%d rule(s) left out", site.HostingRulesLeftOut),
			"The static server applies plain redirects, rewrites and headers from _redirects, _headers, netlify.toml and vercel.json; rules with placeholders, conditions, a proxy to another host or a header it will not write are left out.",
			"Rewrite them as plain path rules, serve them from an application, or accept that those paths answer 404.", "deploy", "configuration.build"))
	}
	if site.HostingRules > 0 {
		findings = append(findings, finding("static_hosting_rules", PreflightPass,
			"Hosting rules are applied by the static server", fmt.Sprintf("%d redirect, rewrite and header rule(s)", site.HostingRules),
			"The redirects and headers the repository declared for another host are written into nginx's configuration.", "", "deploy", "configuration.build"))
	}
	return findings
}
