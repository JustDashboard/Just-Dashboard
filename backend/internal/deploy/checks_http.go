package deploy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// An HTTP readiness probe dials the candidate on loopback, but the
// application answers it the way it answers the proxy only if the request
// looks like the proxy's. A probe carrying Host 127.0.0.1 and no forwarded
// scheme was refused by exactly the settings a production deployment is told
// to use: Django's ALLOWED_HOSTS answered 400, Rails' force_ssl and Django's
// SECURE_SSL_REDIRECT answered a redirect to https that the probe would not
// follow. The planned domain, when the release has one, is therefore sent as
// Host and X-Forwarded-Host with the domain's scheme as X-Forwarded-Proto —
// the headers the managed proxy sets — while the connection still goes only
// to the candidate.

// readinessMaxRedirects bounds how many redirects on the candidate are
// followed before the last one is reported as a loop.
const readinessMaxRedirects = 4

// readinessAccept is what a browser asks for, so an application that
// negotiates content (a GraphQL server's landing page, an API's HTML error
// page) answers the probe the way it answers a visitor.
const readinessAccept = "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8"

func (r *CheckRunner) httpAttempt(
	ctx context.Context,
	kind CheckKind,
	config CheckConfiguration,
	target CheckTarget,
	result *CheckAttemptEvidence,
) {
	address := config.URL
	if address == "" && kind == CheckPublicRoute && len(target.PublicURLs) > 0 {
		address = target.PublicURLs[0]
	}
	var planned []*url.URL
	if address == "" {
		host, port := targetAddress(config, target)
		if host == "" || port == 0 {
			result.Outcome, result.Code = HealthUnavailable, "target_unavailable"
			return
		}
		path := config.Path
		if path == "" {
			path = "/"
		}
		address = "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
		// A check aimed at another host is the operator's own probe of
		// something else; only the candidate is introduced as the domain.
		if kind == CheckHTTP && (config.Host == "" || config.Host == target.Host) {
			planned = plannedOrigins(target.PublicURLs)
		}
	}
	method := config.Method
	if method == "" {
		method = http.MethodGet
	}
	candidate, err := url.Parse(address)
	if err != nil || candidate.Host == "" {
		result.Code = "invalid_target"
		return
	}
	result.Address = address
	var origin *url.URL
	if len(planned) > 0 {
		origin = planned[0]
	}
	// Listed statuses and any-answer checks judge the first answer: a
	// redirect is itself the answer they were written to accept or refuse.
	follow := len(config.ExpectedStatus) == 0 && !config.AcceptAnyAnswer
	current := candidate
	for hop := 0; ; hop++ {
		request, err := http.NewRequestWithContext(ctx, method, current.String(), nil)
		if err != nil {
			result.Code = "invalid_target"
			return
		}
		request.Header.Set("Accept", readinessAccept)
		request.Header.Set("User-Agent", "JustDashboard-Readiness/1")
		if origin != nil {
			request.Host = origin.Host
			request.Header.Set("X-Forwarded-Host", origin.Host)
			request.Header.Set("X-Forwarded-Proto", origin.Scheme)
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
		}
		response, err := r.http.Do(request)
		if err != nil {
			result.Code = checkNetworkError(ctx, err)
			return
		}
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		_ = response.Body.Close()
		result.StatusCode = response.StatusCode
		result.RedirectOrigin = ""
		if !follow || !redirectStatus(response.StatusCode) || response.Header.Get("Location") == "" {
			break
		}
		location, err := current.Parse(response.Header.Get("Location"))
		if err != nil {
			break
		}
		next, nextOrigin, onCandidate := redirectOnCandidate(candidate, location, planned)
		result.RedirectOrigin = location.Scheme + "://" + location.Host
		if !onCandidate {
			result.Code = "redirect_off_origin"
			break
		}
		if hop >= readinessMaxRedirects {
			result.Code = "redirect_loop"
			break
		}
		current = next
		if nextOrigin != nil {
			origin = nextOrigin
		}
	}
	accepted := expectedHTTPStatus(result.StatusCode, config.ExpectedStatus)
	if config.AcceptAnyAnswer {
		accepted = answeredHTTPStatus(result.StatusCode)
	}
	if accepted {
		result.Outcome, result.Code, result.RedirectOrigin = HealthPassed, "", ""
	} else if result.Code == "" {
		result.Code = "unexpected_status"
	}
}

// plannedOrigins are the release's own public origins: the scheme and host a
// visitor uses, which is what the proxy forwards to the application.
func plannedOrigins(publicURLs []string) []*url.URL {
	origins := make([]*url.URL, 0, len(publicURLs))
	for _, raw := range publicURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		origins = append(origins, &url.URL{Scheme: parsed.Scheme, Host: strings.ToLower(parsed.Host)})
	}
	return origins
}

// redirectOnCandidate decides whether a redirect stays on the release being
// checked. A redirect on the candidate's own address is followed as it is; a
// redirect to one of the release's planned domains is the application naming
// itself by its public name — a locale prefix, a login page, the https form of
// the same URL — so the same path is asked of the candidate again, never of
// the domain, which may still route to the previous release. Anything else is
// another site and is never requested.
func redirectOnCandidate(candidate, location *url.URL, planned []*url.URL) (*url.URL, *url.URL, bool) {
	if location.Scheme == candidate.Scheme && strings.EqualFold(location.Host, candidate.Host) {
		return location, nil, true
	}
	if location.Scheme != "http" && location.Scheme != "https" {
		return nil, nil, false
	}
	for _, origin := range planned {
		if !sameHostAndPort(location, origin) {
			continue
		}
		next := *candidate
		next.Path, next.RawPath, next.RawQuery = location.Path, location.RawPath, location.RawQuery
		if next.Path == "" {
			next.Path = "/"
		}
		return &next, origin, true
	}
	return nil, nil, false
}

// sameHostAndPort compares a redirect's authority with a planned origin's,
// treating an absent port as the scheme's default. The scheme itself may
// differ: an application that does not trust X-Forwarded-Proto names its own
// domain with http.
func sameHostAndPort(location, origin *url.URL) bool {
	if !strings.EqualFold(location.Hostname(), origin.Hostname()) {
		return false
	}
	port := func(value *url.URL) string {
		if value.Port() != "" {
			return value.Port()
		}
		if value.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	if location.Port() == "" && origin.Port() == "" {
		return true
	}
	return port(location) == port(origin)
}

func redirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// answeredHTTPStatus is what an any-answer check accepts. A 400 and a 421 are
// left out because they are how host allowlists refuse a request (Django's
// DisallowedHost, ASP.NET host filtering, a misdirected TLS name): accepting
// them would let a release that refuses its own domain go live.
func answeredHTTPStatus(status int) bool {
	return status >= 100 && status < 500 && status != http.StatusBadRequest && status != http.StatusMisdirectedRequest
}
