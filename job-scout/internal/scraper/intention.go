package scraper

import (
	"strings"

	"github.com/jobscout/jobscout/internal/db"
)

// DeriveSearchIntention maps provider search flags to a work-type intention.
// LinkedIn uses f_WT codes. Indeed uses the per-run remote filter. Dice uses
// include_remote. Empty / unrestricted LinkedIn searches are onsite.
func DeriveSearchIntention(source db.JobSource, query map[string]string, indeedFilter string) string {
	switch source {
	case db.SourceIndeed:
		switch strings.ToLower(strings.TrimSpace(indeedFilter)) {
		case "remote":
			return db.SearchIntentionRemote
		case "hybrid":
			return db.SearchIntentionHybrid
		default:
			return db.SearchIntentionOnsite
		}
	case db.SourceDice:
		if strings.EqualFold(strings.TrimSpace(query["include_remote"]), "true") {
			return db.SearchIntentionRemote
		}
		return db.SearchIntentionOnsite
	case db.SourceFantastic:
		return fantasticSearchIntention(query["aiWorkArrangementFilter"])
	default:
		return linkedInSearchIntention(query["f_WT"])
	}
}

func fantasticSearchIntention(raw string) string {
	hasOnsite := false
	hasRemote := false
	hasHybrid := false
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "On-site":
			hasOnsite = true
		case "Remote OK", "Remote Solely":
			hasRemote = true
		case "Hybrid":
			hasHybrid = true
		}
	}
	if hasOnsite {
		return db.SearchIntentionOnsite
	}
	if hasRemote && hasHybrid {
		return db.SearchIntentionRemoteHybrid
	}
	if hasRemote {
		return db.SearchIntentionRemote
	}
	if hasHybrid {
		return db.SearchIntentionHybrid
	}
	return db.SearchIntentionOnsite
}

func linkedInSearchIntention(fWT string) string {
	codes := parseLinkedInWorkTypes(fWT)
	if len(codes) == 0 {
		return db.SearchIntentionOnsite
	}
	hasOnsite := false
	hasRemote := false
	hasHybrid := false
	for _, code := range codes {
		switch code {
		case "1":
			hasOnsite = true
		case "2":
			hasRemote = true
		case "3":
			hasHybrid = true
		}
	}
	if hasOnsite {
		return db.SearchIntentionOnsite
	}
	if hasRemote && hasHybrid {
		return db.SearchIntentionRemoteHybrid
	}
	if hasRemote {
		return db.SearchIntentionRemote
	}
	if hasHybrid {
		return db.SearchIntentionHybrid
	}
	return db.SearchIntentionOnsite
}
