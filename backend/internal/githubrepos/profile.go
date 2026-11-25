package githubrepos

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Profile is GitHubProfile: the connected account as its GitHub profile
// draws it. Everything GitHub holds as empty is absent here, never an empty
// string for the page to test.
type Profile struct {
	Login    string `json:"login"`
	Name     string `json:"name,omitempty"`
	Bio      string `json:"bio,omitempty"`
	Company  string `json:"company,omitempty"`
	Location string `json:"location,omitempty"`
	// Blog is the website on the profile as GitHub holds it, which may have
	// no scheme.
	Blog        string    `json:"blog,omitempty"`
	HTMLURL     string    `json:"htmlUrl"`
	Followers   int       `json:"followers"`
	Following   int       `json:"following"`
	PublicRepos int       `json:"publicRepos"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
	// Contributions is absent when GitHub would not answer for the calendar;
	// ContributionsMessage then says so.
	Contributions        *Contributions `json:"contributions,omitempty"`
	ContributionsMessage string         `json:"contributionsMessage,omitempty"`
}

// Contributions is GitHub's contribution calendar: week columns, oldest
// first, each Sunday to Saturday.
type Contributions struct {
	Total int                `json:"total"`
	Weeks []ContributionWeek `json:"weeks"`
}

type ContributionWeek struct {
	Days []ContributionDay `json:"days"`
}

type ContributionDay struct {
	// Date is YYYY-MM-DD.
	Date  string `json:"date"`
	Count int    `json:"count"`
	// Level is GitHub's quartile, 0 for none to 4 for the busiest days.
	Level int `json:"level"`
	// Weekday is 0 for Sunday.
	Weekday int `json:"weekday"`
}

// rawProfile is GET /user. GitHub answers null for what a person left empty.
type rawProfile struct {
	Login       string     `json:"login"`
	Name        *string    `json:"name"`
	Bio         *string    `json:"bio"`
	Company     *string    `json:"company"`
	Location    *string    `json:"location"`
	Blog        *string    `json:"blog"`
	HTMLURL     string     `json:"html_url"`
	Followers   int        `json:"followers"`
	Following   int        `json:"following"`
	PublicRepos int        `json:"public_repos"`
	CreatedAt   *time.Time `json:"created_at"`
}

func (raw rawProfile) profile() Profile {
	text := func(value *string) string { return strings.TrimSpace(deref(value)) }
	return Profile{
		Login: raw.Login, Name: text(raw.Name), Bio: text(raw.Bio), Company: text(raw.Company),
		Location: text(raw.Location), Blog: text(raw.Blog), HTMLURL: webURL(raw.HTMLURL),
		Followers: raw.Followers, Following: raw.Following, PublicRepos: raw.PublicRepos, CreatedAt: timeOf(raw.CreatedAt),
	}
}

// contributionsQuery asks for the viewer's last year, which is what
// contributionsCollection covers when given no dates.
const contributionsQuery = `query { viewer { contributionsCollection { contributionCalendar { totalContributions weeks { contributionDays { date contributionCount contributionLevel weekday } } } } } }`

type rawContributions struct {
	Viewer *struct {
		ContributionsCollection *struct {
			ContributionCalendar *struct {
				TotalContributions int `json:"totalContributions"`
				Weeks              []struct {
					ContributionDays []struct {
						Date              string `json:"date"`
						ContributionCount int    `json:"contributionCount"`
						ContributionLevel string `json:"contributionLevel"`
						Weekday           int    `json:"weekday"`
					} `json:"contributionDays"`
				} `json:"weeks"`
			} `json:"contributionCalendar"`
		} `json:"contributionsCollection"`
	} `json:"viewer"`
}

// contributionLevel is GitHub's quartile name as the 0 to 4 the calendar is
// shaded by. A name GitHub adds later reads as no shade rather than a guess.
func contributionLevel(name string) int {
	switch name {
	case "FIRST_QUARTILE":
		return 1
	case "SECOND_QUARTILE":
		return 2
	case "THIRD_QUARTILE":
		return 3
	case "FOURTH_QUARTILE":
		return 4
	}
	return 0
}

func (raw rawContributions) contributions() (*Contributions, bool) {
	if raw.Viewer == nil || raw.Viewer.ContributionsCollection == nil || raw.Viewer.ContributionsCollection.ContributionCalendar == nil {
		return nil, false
	}
	calendar := raw.Viewer.ContributionsCollection.ContributionCalendar
	out := &Contributions{Total: calendar.TotalContributions, Weeks: make([]ContributionWeek, 0, len(calendar.Weeks))}
	for _, week := range calendar.Weeks {
		days := make([]ContributionDay, 0, len(week.ContributionDays))
		for _, day := range week.ContributionDays {
			date, err := time.Parse(time.DateOnly, day.Date)
			if err != nil {
				continue
			}
			weekday := day.Weekday
			if weekday < 0 || weekday > 6 {
				weekday = int(date.Weekday())
			}
			days = append(days, ContributionDay{Date: day.Date, Count: day.ContributionCount, Level: contributionLevel(day.ContributionLevel), Weekday: weekday})
		}
		out.Weeks = append(out.Weeks, ContributionWeek{Days: days})
	}
	return out, true
}

func readContributions(ctx context.Context, client *Client) (*Contributions, error) {
	var raw rawContributions
	if err := client.GraphQL(ctx, contributionsQuery, &raw); err != nil {
		return nil, err
	}
	calendar, ok := raw.contributions()
	if !ok {
		return nil, &GraphQLError{Messages: []string{"GitHub answered without a contribution calendar"}}
	}
	return calendar, nil
}

// accountFailure reports whether err is about the account rather than the one
// thing asked for: a sign-in GitHub no longer accepts, or a spent rate limit.
// Those are the whole answer wherever they happen.
func accountFailure(err error) bool {
	switch KindOf(err) {
	case KindUnauthorized, KindRateLimited:
		return true
	}
	var reconnectErr *ReconnectError
	return errors.As(err, &reconnectErr)
}

// contributionsWords says why the calendar is missing from a profile that
// still answered.
func contributionsWords(err error) string {
	var refused *GraphQLError
	if errors.As(err, &refused) || unreadable(err) {
		return "GitHub did not share the contribution calendar."
	}
	return "The contribution calendar could not be read from GitHub just now."
}

// readProfile is the profile from REST and the calendar from GraphQL. The
// calendar is the lesser half: when GitHub will not answer for it the
// profile still stands, with the reason in ContributionsMessage.
func readProfile(ctx context.Context, client *Client) (Profile, error) {
	var raw rawProfile
	if _, err := client.Do(ctx, http.MethodGet, "/user", nil, &raw); err != nil {
		return Profile{}, err
	}
	if raw.Login == "" {
		return Profile{}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub did not say whose profile this is", Path: "/user"}
	}
	profile := raw.profile()
	calendar, err := readContributions(ctx, client)
	switch {
	case err == nil:
		profile.Contributions = calendar
	case ctx.Err() != nil:
		return Profile{}, ctx.Err()
	case accountFailure(err):
		return Profile{}, err
	default:
		profile.ContributionsMessage = contributionsWords(err)
	}
	return profile, nil
}

// Profile is the connected account's profile and its last year of
// contributions, answered from memory for five minutes unless refresh is
// set. A profile that came back without its calendar is kept only a minute,
// so a calendar GitHub could not answer for once is asked for again soon.
func (s *Service) Profile(ctx context.Context, userID, id int64, refresh bool) (Profile, error) {
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return Profile{}, err
	}
	if !refresh {
		s.mu.Lock()
		cached, ok := s.profiles.get(id, s.now())
		s.mu.Unlock()
		if ok {
			return cached, nil
		}
	}
	profile, err := readProfile(ctx, s.client(c))
	if err != nil {
		return Profile{}, err
	}
	if profile.HTMLURL == "" {
		profile.HTMLURL = c.HTMLURL
	}
	ttl := profileTTL
	if profile.Contributions == nil {
		ttl = listTTL
	}
	s.mu.Lock()
	s.profiles.put(id, profile, s.now(), ttl)
	s.mu.Unlock()
	return profile, nil
}
