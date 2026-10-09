package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
)

func (c *nativeClient) inspectAdGuardOverrides(ctx context.Context, s *Snapshot) error {
	var overrides []struct {
		Name  string `json:"domain"`
		Value string `json:"answer"`
	}
	err := c.request(ctx, http.MethodGet, "/control/rewrite/list", nil, &overrides)
	if err != nil || overrides == nil {
		s.OverrideEvidence = Reading{"unknown", "unavailable", "Native rewrite inventory is unavailable or was not declared."}
		return nil
	}
	if len(overrides) > 256 {
		return errors.New("native AdGuard rewrite inventory exceeds its bound")
	}
	for _, override := range overrides {
		s.LocalOverrides = append(s.LocalOverrides, LocalOverride{c.text(override.Name), c.text(override.Value), "native_rewrite"})
	}
	s.OverrideEvidence = Reading{"configured", "native_configuration", "Native DNS rewrites are local answer overrides, separate from delegated authoritative zones."}
	return c.retainPolicy(overrides)
}

// Only installed, recognized native apps are read. This does not install apps,
// expose a generic config proxy, or turn their settings into live path evidence.
func (c *nativeClient) inspectNativeGroups(ctx context.Context, s *Snapshot, name string, views bool) error {
	if len(name) == 0 || len(name) > 128 {
		return errors.New("native app identity exceeds its bound")
	}
	var response struct {
		Config *string `json:"config"`
	}
	err := c.technitium(ctx, "/api/apps/config/get", url.Values{"name": {name}}, &response)
	reading := Reading{"unknown", "unavailable", "The installed native app did not provide a readable configuration."}
	if err != nil || response.Config == nil {
		if views {
			s.Views = reading
		} else {
			s.AppClientEvidence = reading
		}
		return nil
	}
	if len(*response.Config) > 128<<10 {
		return errors.New("native app configuration exceeds its bound")
	}
	var raw map[string]json.RawMessage
	var config struct {
		Blocking       *bool               `json:"enableBlocking"`
		Translation    *bool               `json:"enableAddressTranslation"`
		Networks       map[string][]string `json:"networks"`
		NetworkGroups  map[string]string   `json:"networkGroupMap"`
		ListenerGroups map[string]string   `json:"localEndPointGroupMap"`
		DomainGroups   map[string]string   `json:"domainGroupMap"`
		Groups         []struct {
			Name         string            `json:"name"`
			Blocking     *bool             `json:"enableBlocking"`
			Enabled      *bool             `json:"enabled"`
			Translations map[string]string `json:"externalToInternalTranslation"`
		} `json:"groups"`
	}
	if json.Unmarshal([]byte(*response.Config), &raw) != nil || json.Unmarshal([]byte(*response.Config), &config) != nil || config.Groups == nil {
		if views {
			s.Views = reading
		} else {
			s.AppClientEvidence = reading
		}
		return nil
	}
	if len(config.Groups) > 64 || len(config.Networks) > 128 || len(config.NetworkGroups) > 128 || len(config.ListenerGroups) > 128 || len(config.DomainGroups) > 128 {
		return errors.New("native app groups exceed their bound")
	}
	for _, scopes := range config.Networks {
		if len(scopes) > 128 {
			return errors.New("native named network exceeds its bound")
		}
	}
	if views {
		s.TranslationEnabled = config.Translation
		for name, scopes := range config.Networks {
			values, e := c.strings(scopes, 128)
			if e != nil {
				return e
			}
			s.NamedNetworks[c.text(name)] = values
		}
		s.Views = Reading{"configured", "native_app_configuration", "Installed Split Horizon network groups and address translations; per-zone APP record answers and actual client selection remain unmeasured."}
	} else {
		s.AppProtection = config.Blocking
		s.AppClientEvidence = Reading{"configured", "native_app_configuration", "Installed Advanced Blocking client/listener groups are independent of the built-in blocking switch. Filtering rule content and actual client selection remain native."}
	}
	for _, group := range config.Groups {
		if len(group.Translations) > 128 {
			return errors.New("native address translations exceed their bound")
		}
		g := NativeGroup{Name: c.text(group.Name), Enabled: group.Blocking, ClientScopes: []string{}, ListenerScopes: []string{}, Domains: []string{}}
		if views {
			g.Enabled = group.Enabled
			g.Translations = map[string]string{}
			for from, to := range group.Translations {
				g.Translations[c.text(from)] = c.text(to)
			}
		}
		for scope, target := range config.NetworkGroups {
			if target == group.Name {
				g.ClientScopes = append(g.ClientScopes, c.text(scope))
			}
		}
		for scope, target := range config.ListenerGroups {
			if target == group.Name {
				g.ListenerScopes = append(g.ListenerScopes, c.text(scope))
			}
		}
		for domain, target := range config.DomainGroups {
			if target == group.Name {
				g.Domains = append(g.Domains, c.text(domain))
			}
		}
		sort.Strings(g.ClientScopes)
		sort.Strings(g.ListenerScopes)
		sort.Strings(g.Domains)
		if views {
			s.ViewGroups = append(s.ViewGroups, g)
		} else {
			s.FilterGroups = append(s.FilterGroups, g)
		}
	}
	return c.retainPolicy(raw)
}

func (c *nativeClient) inspectPiHoleGroups(ctx context.Context, s *Snapshot) error {
	var response struct {
		Groups []struct {
			ID      *int   `json:"id"`
			Name    string `json:"name"`
			Enabled *bool  `json:"enabled"`
		} `json:"groups"`
	}
	err := c.request(ctx, http.MethodGet, "/api/groups", nil, &response)
	if err != nil || response.Groups == nil {
		s.AppClientEvidence = Reading{"unknown", "unavailable", "Native FTL client filter-group inventory is unavailable or was not declared."}
		return nil
	}
	if len(response.Groups) > 128 {
		return errors.New("native FTL filter groups exceed their bound")
	}
	for _, group := range response.Groups {
		if group.ID == nil || group.Enabled == nil {
			s.AppClientEvidence = Reading{"unknown", "unavailable", "Native FTL group identity or enable state is unreadable."}
			return nil
		}
		s.FilterGroups = append(s.FilterGroups, NativeGroup{ID: group.ID, Name: c.text(group.Name), Enabled: group.Enabled, ClientScopes: []string{}, ListenerScopes: []string{}, Domains: []string{}})
	}
	s.AppClientEvidence = Reading{"configured", "native_configuration", "Native FTL filter-group IDs and enable states are listed independently of the global blocking switch and client assignments. Actual filtering decisions remain native."}
	return c.retainPolicy(response)
}
