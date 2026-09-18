package domain

import (
	"testing"
	"time"
)

func TestIsDue_MatchesSpecArithmetic(t *testing.T) {
	now := time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC)
	interval := 900
	past := now.Add(-time.Hour)
	future := now.Add(5 * time.Minute)
	lastDue := now.Add(-time.Duration(interval) * time.Second)
	lastNotDue := now.Add(-time.Duration(interval-1) * time.Second)

	tests := []struct {
		name  string
		p     ProviderCadence
		force bool
		want  bool
	}{
		{
			name: "disabled never scraped",
			p:    ProviderCadence{Source: "INDEED", Enabled: false, ScrapeIntervalSeconds: interval},
			want: false,
		},
		{
			name:  "disabled force does not scrape",
			p:     ProviderCadence{Source: "INDEED", Enabled: false, ScrapeIntervalSeconds: interval},
			force: true,
			want:  false,
		},
		{
			name:  "enabled force ignores interval and backoff",
			p:     ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(now), NextEligibleAt: ptrTime(future)},
			force: true,
			want:  true,
		},
		{
			name: "enabled never scraped no backoff",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval},
			want: true,
		},
		{
			name: "enabled never scraped backoff past",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, NextEligibleAt: ptrTime(past)},
			want: true,
		},
		{
			name: "enabled never scraped backoff future",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, NextEligibleAt: ptrTime(future)},
			want: false,
		},
		{
			name: "enabled interval elapsed no backoff",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(lastDue)},
			want: true,
		},
		{
			name: "enabled interval not elapsed",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(lastNotDue)},
			want: false,
		},
		{
			name: "enabled interval elapsed backoff future",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(lastDue.Add(-time.Minute)), NextEligibleAt: ptrTime(future)},
			want: false,
		},
		{
			name: "enabled interval elapsed backoff past",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(lastDue.Add(-time.Minute)), NextEligibleAt: ptrTime(past)},
			want: true,
		},
		{
			name: "enabled interval elapsed backoff equal now",
			p:    ProviderCadence{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: interval, LastScrapedAt: ptrTime(lastDue.Add(-time.Minute)), NextEligibleAt: ptrTime(now)},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDue(tt.p, now, tt.force)
			if got != tt.want {
				t.Errorf("IsDue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDueProviders_KeepsInputOrderAndSkipsDisabled(t *testing.T) {
	now := time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC)
	providers := []ProviderCadence{
		{Source: "INDEED", Enabled: false, ScrapeIntervalSeconds: 900},
		{Source: "LINKEDIN", Enabled: true, ScrapeIntervalSeconds: 900},
	}

	got := DueProviders(providers, now, false)
	if len(got) != 1 || got[0].Source != "LINKEDIN" {
		t.Errorf("DueProviders() = %v, want only enabled due LINKEDIN", sourcesOf(got))
	}

	forced := DueProviders(providers, now, true)
	if len(forced) != 1 || forced[0].Source != "LINKEDIN" {
		t.Errorf("DueProviders(force) = %v, want LINKEDIN only (Indeed stays disabled)", sourcesOf(forced))
	}
}

func sourcesOf(providers []ProviderCadence) []string {
	out := make([]string, len(providers))
	for i, p := range providers {
		out[i] = p.Source
	}
	return out
}

func ptrTime(t time.Time) *time.Time { return &t }
