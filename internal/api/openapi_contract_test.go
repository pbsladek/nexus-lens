package api

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIIncludesRetentionCleanupAndErrorContracts(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}

	requiredPaths := map[string][]string{
		"/api/v1/admin/retention-settings":                         {"get", "put"},
		"/api/v1/cleanup/simulations":                              {"post"},
		"/api/v1/cleanup/executions":                               {"get", "post"},
		"/api/v1/cleanup/executions/{cleanup_execution_id}/cancel": {"post"},
		"/api/v1/cleanup/executions/{cleanup_execution_id}/events": {"get"},
	}
	for path, methods := range requiredPaths {
		operations, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("OpenAPI path %s missing", path)
		}
		for _, method := range methods {
			if _, ok := operations[method]; !ok {
				t.Fatalf("OpenAPI path %s method %s missing", path, method)
			}
		}
	}

	for _, schema := range []string{
		"Problem",
		"RetentionSettings",
		"CleanupSimulationRequest",
		"CleanupSimulationResult",
		"CleanupExecutionRequest",
		"CleanupExecution",
		"CleanupExecutionEvent",
	} {
		if _, ok := spec.Components.Schemas[schema]; !ok {
			t.Fatalf("OpenAPI schema %s missing", schema)
		}
	}
}
