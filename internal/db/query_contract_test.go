package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyticsQueryContractsForLargeResultSets(t *testing.T) {
	root := filepath.Join("..", "..")
	analytics := readRepoFile(t, root, "db", "queries", "analytics.sql")
	tagPatterns := readRepoFile(t, root, "db", "queries", "tag_patterns.sql")
	performanceIndexes := readRepoFile(t, root, "db", "migrations", "000020_performance_review_indexes.sql")

	for _, queryName := range []string{
		"ListRepositoryAnalytics",
		"ListImageAnalytics",
		"ListTagAnalytics",
	} {
		query := namedSQLBlock(t, analytics, queryName)
		assertContains(t, query, "LIMIT sqlc.arg(limit_count)")
		assertContains(t, query, "ORDER BY")
		assertNotContains(t, query, "OFFSET")
	}

	for _, queryName := range []string{
		"ListTagPatternAnalytics",
		"ListTagPatternGrowth",
		"ListTagPatternExamples",
		"ListTagPatternSuggestions",
	} {
		query := namedSQLBlock(t, tagPatterns, queryName)
		assertContains(t, query, "LIMIT sqlc.arg(limit_count)")
		assertContains(t, query, "ORDER BY")
		assertNotContains(t, query, "OFFSET")
	}

	for _, indexName := range []string{
		"repositories_format_type_nexus_name_idx",
		"container_tags_active_image_first_seen_idx",
		"container_tags_active_image_last_seen_idx",
		"nexus_assets_repo_checksum_downloaded_idx",
	} {
		assertContains(t, performanceIndexes, indexName)
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(body)
}

func namedSQLBlock(t *testing.T, body, name string) string {
	t.Helper()
	marker := "-- name: " + name + " "
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("query %s not found", name)
	}
	rest := body[start+len(marker):]
	next := strings.Index(rest, "\n-- name: ")
	if next >= 0 {
		return rest[:next]
	}
	return rest
}

func assertContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("expected SQL block to contain %q:\n%s", want, body)
	}
}

func assertNotContains(t *testing.T, body, unwanted string) {
	t.Helper()
	if strings.Contains(body, unwanted) {
		t.Fatalf("expected SQL block not to contain %q:\n%s", unwanted, body)
	}
}
