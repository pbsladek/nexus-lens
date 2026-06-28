package indexing

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

type taskScope struct {
	Repositories []string `json:"repositories,omitempty"`
	WindowDays   int      `json:"window_days,omitempty"`
	Since        string   `json:"since,omitempty"`
}

func normalizeRunScope(scanType string, scope RunScope, now time.Time) taskScope {
	out := taskScope{
		Repositories: normalizeRepositories(scope.Repositories),
		WindowDays:   scope.WindowDays,
	}
	if scanType == ScanTypeBoundedReindex {
		if out.WindowDays <= 0 {
			out.WindowDays = 7
		}
		since := boundedWindowStart(now.UTC(), out.WindowDays)
		out.Since = since.Format(time.RFC3339)
	}
	return out
}

func decodeTaskScope(payload []byte) taskScope {
	var envelope struct {
		Scope taskScope `json:"scope"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return taskScope{}
	}
	return envelope.Scope
}

func normalizeRepositories(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func boundedWindowStart(now time.Time, windowDays int) time.Time {
	if windowDays <= 0 {
		windowDays = 7
	}
	year, month, day := now.Date()
	todayUTC := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return todayUTC.AddDate(0, 0, -windowDays)
}

func repositoryAllowed(repository string, scope taskScope) bool {
	if len(scope.Repositories) == 0 {
		return true
	}
	_, ok := slices.BinarySearch(scope.Repositories, repository)
	return ok
}

func scopeSince(scope taskScope) (time.Time, bool) {
	if scope.Since == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, scope.Since)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
