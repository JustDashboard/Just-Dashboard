package dnsrecords

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Target is where a name's records are written: a provider, and the zone
// of its that holds the name.
type Target struct {
	Provider Provider
	Zone     Zone
}

// Candidate is one provider as Resolve asked it: the zone it reaches for the
// name, or why it could not answer.
type Candidate struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	Zone     string `json:"zone,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Refusal is why no saved credential can write a name, with a code a client
// can branch on.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Refusal codes.
const (
	// RefusedNoCredentials: no DNS provider has credentials saved.
	RefusedNoCredentials = "no_credentials"
	// RefusedNoZone: none of the saved credentials reaches a zone that
	// holds the name.
	RefusedNoZone = "no_zone"
	// RefusedAmbiguous: two providers hold the same zone and the zone's
	// nameservers name neither.
	RefusedAmbiguous = "ambiguous"
)

// CertificatesPage is where credentials are saved, in the navigation's
// words and as an address, so a refusal read anywhere leads there.
const CertificatesPage = "Proxy → Certificates → DNS challenge providers (/proxy/certificates)"

// Resolve asks every provider which of its zones holds name, at the same
// time and each within its own bound, and answers the provider whose zone is
// the longest: a sub-zone delegated to another provider is the one whose
// servers answer for the name. Two providers holding the same zone — a move
// between providers half done — are told apart by hostedBy, which names the
// provider key the zone's nameservers belong to; with no answer there it is
// a refusal rather than a guess, since a record written to the zone nobody
// serves changes nothing anyone sees.
func Resolve(ctx context.Context, name string, providers []Provider, hostedBy func(context.Context, string) string) (Target, []Candidate, error) {
	name = Canonical(name)
	if len(providers) == 0 {
		return Target{}, []Candidate{}, &Refusal{Code: RefusedNoCredentials, Message: fmt.Sprintf(
			"No DNS provider has credentials saved on this server, so the record for %s cannot be written from here. Save your DNS provider's API token under %s, or create the record where the zone is kept.",
			name, CertificatesPage)}
	}
	candidates := make([]Candidate, len(providers))
	zones := make([]*Zone, len(providers))
	var wait sync.WaitGroup
	for index, provider := range providers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			candidates[index] = Candidate{Key: provider.Key(), Provider: provider.Name()}
			askCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			reached, err := provider.Zones(askCtx, name)
			if err != nil {
				candidates[index].Error = err.Error()
				return
			}
			if zone, ok := ZoneFor(name, reached); ok {
				candidates[index].Zone = Canonical(zone.Name)
				zones[index] = &zone
			}
		}()
	}
	wait.Wait()
	best := -1
	tied := []int{}
	for index, zone := range zones {
		if zone == nil {
			continue
		}
		switch {
		case best < 0 || len(Canonical(zone.Name)) > len(Canonical(zones[best].Name)):
			best, tied = index, []int{index}
		case Canonical(zone.Name) == Canonical(zones[best].Name):
			tied = append(tied, index)
		}
	}
	if best < 0 {
		asked := []string{}
		for _, candidate := range candidates {
			if candidate.Error != "" {
				asked = append(asked, candidate.Provider+" could not be asked ("+candidate.Error+")")
			} else {
				asked = append(asked, candidate.Provider+" reaches no zone holding it")
			}
		}
		return Target{}, candidates, &Refusal{Code: RefusedNoZone, Message: fmt.Sprintf(
			"None of the DNS credentials saved here reaches a zone that holds %s: %s. Save credentials for the provider that hosts the zone under %s, or create the record where the zone is kept.",
			name, strings.Join(asked, "; "), CertificatesPage)}
	}
	if len(tied) > 1 {
		host := ""
		if hostedBy != nil {
			host = hostedBy(ctx, name)
		}
		for _, index := range tied {
			if providers[index].Key() == host {
				return Target{Provider: providers[index], Zone: *zones[index]}, candidates, nil
			}
		}
		names := make([]string, 0, len(tied))
		for _, index := range tied {
			names = append(names, providers[index].Name())
		}
		return Target{}, candidates, &Refusal{Code: RefusedAmbiguous, Message: fmt.Sprintf(
			"%s each hold a zone %s, and its nameservers name neither, so which one serves %s cannot be told from here. Remove the credentials of the one that does not under %s, or create the record where the zone is served.",
			strings.Join(names, " and "), Canonical(zones[best].Name), name, CertificatesPage)}
	}
	return Target{Provider: providers[best], Zone: *zones[best]}, candidates, nil
}
