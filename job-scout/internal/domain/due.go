// Package domain holds scrape cadence rules, notify filters, reject reasons,
// and message_to_send formatting.
// Adapters stay in api, pipeline, scraper, db, and config.
package domain

import "time"

// ProviderCadence is the cadence state for one job source.
type ProviderCadence struct {
	Source                string
	Enabled               bool
	ScrapeIntervalSeconds int
	LastScrapedAt         *time.Time
	NextEligibleAt        *time.Time
}

// IsDue reports whether a provider should scrape now.
func IsDue(p ProviderCadence, now time.Time, force bool) bool {
	if !p.Enabled {
		return false
	}
	if force {
		return true
	}
	interval := time.Duration(p.ScrapeIntervalSeconds) * time.Second
	if p.LastScrapedAt == nil {
		return p.NextEligibleAt == nil || !p.NextEligibleAt.After(now)
	}
	if now.Before(p.LastScrapedAt.Add(interval)) {
		return false
	}
	if p.NextEligibleAt != nil && now.Before(*p.NextEligibleAt) {
		return false
	}
	return true
}

// DueProviders returns enabled providers that are due, in input order.
func DueProviders(providers []ProviderCadence, now time.Time, force bool) []ProviderCadence {
	out := make([]ProviderCadence, 0, len(providers))
	for _, p := range providers {
		if IsDue(p, now, force) {
			out = append(out, p)
		}
	}
	return out
}
