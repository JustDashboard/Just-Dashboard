package proxysvc

import (
	"fmt"
	"math"
	"time"
)

// When a certificate is due for renewal.
//
// A fixed thirty days was right for Let's Encrypt's ninety-day certificates
// and wrong for every other term: a six-day certificate is inside thirty days
// from the moment it is issued, so the Certificates page called it expiring
// for its whole life, while the TLS report used fourteen days and told the
// same certificate's owner that renewal "starts at 30 days left, so this one
// is not renewing". certbot (since 4.0) and Caddy renew once a third of a
// certificate's term is left, half of it for a term of ten days or less. That
// is the window here, and both pages read it, so they never disagree about one
// certificate. It never opens more than thirty days out: a year-long
// certificate renewed by hand is flagged a month ahead, as it always was, and
// not four months ahead.

const maxRenewalWindow = 30 * 24 * time.Hour

// renewalWindow is how long before notAfter a certificate is due for renewal.
// A term that makes no sense (notBefore missing or not before notAfter) gets
// the thirty days every certificate used to.
func renewalWindow(notBefore, notAfter time.Time) time.Duration {
	term := notAfter.Sub(notBefore)
	if notBefore.IsZero() || term <= 0 {
		return maxRenewalWindow
	}
	window := term / 3
	if term <= 10*24*time.Hour {
		window = term / 2
	}
	return min(window, maxRenewalWindow)
}

// renewalDue reports whether a certificate still valid at now has entered its
// renewal window.
func renewalDue(notBefore, notAfter, now time.Time) bool {
	return now.Before(notAfter) && notAfter.Sub(now) <= renewalWindow(notBefore, notAfter)
}

// wholeHours is a span in hours, rounded. X.509's notAfter is the last second
// a certificate is valid, so a ninety-day term measures ninety days less a
// second, which truncating would shorten.
func wholeHours(d time.Duration) int {
	return int(math.Round(d.Hours()))
}

// termShare says where the renewal window sits in the term: "the last 30 of
// its 90 days", or in hours for a term under ten days, which whole days would
// misname — Let's Encrypt's short-lived certificates run 160 hours, neither
// six days nor seven.
func termShare(window, term time.Duration) string {
	if term < 10*24*time.Hour {
		return fmt.Sprintf("the last %d of its %d hours", wholeHours(window), wholeHours(term))
	}
	return fmt.Sprintf("the last %d of its %d days", int(math.Round(window.Hours()/24)), int(math.Round(term.Hours()/24)))
}

// timeLeft says how long until a moment in the unit that reads naturally: days
// while there are two or more, then hours, so a short-lived certificate's last
// day does not read "0 days".
func timeLeft(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "an hour"
	}
	return "less than an hour"
}
