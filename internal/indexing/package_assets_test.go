package indexing

import (
	"testing"

	"github.com/pbsladek/nexus-lens/internal/nexus"
)

func TestGenericArtifactFromAsset(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		path      string
		namespace string
		artifact  string
		version   string
		parser    string
		quality   string
	}{
		{
			name:      "npm scoped package",
			format:    "npm",
			path:      "@scope/pkg/-/pkg-1.2.3.tgz",
			namespace: "@scope",
			artifact:  "pkg",
			version:   "1.2.3",
			parser:    "npm-scoped-tarball",
			quality:   "parsed",
		},
		{
			name:     "npm unscoped package",
			format:   "npm",
			path:     "left-pad/-/left-pad-1.3.0.tgz",
			artifact: "left-pad",
			version:  "1.3.0",
			parser:   "npm-tarball",
			quality:  "parsed",
		},
		{
			name:     "pypi wheel",
			format:   "pypi",
			path:     "packages/example_pkg-2.0.1-py3-none-any.whl",
			artifact: "example_pkg",
			version:  "2.0.1",
			parser:   "pypi-filename",
			quality:  "parsed",
		},
		{
			name:     "go module zip",
			format:   "go",
			path:     "github.com/example/project/@v/v1.2.3.zip",
			artifact: "github.com/example/project",
			version:  "v1.2.3",
			parser:   "go-module-path",
			quality:  "parsed",
		},
		{
			name:     "unknown fallback",
			format:   "rpm",
			path:     "packages/example.rpm",
			artifact: "packages/example.rpm",
			parser:   "path",
			quality:  "path_only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := genericArtifactFromAsset(tt.format, nexus.Asset{Path: tt.path})
			if got.Namespace != tt.namespace ||
				got.Name != tt.artifact ||
				got.Version != tt.version ||
				got.Parser != tt.parser ||
				got.IdentityQuality != tt.quality {
				t.Fatalf("identity = %+v", got)
			}
		})
	}
}

func TestPackageAssetIndexFormat(t *testing.T) {
	for _, format := range []string{"pypi", "npm", "go"} {
		if !packageAssetIndexFormat(format) {
			t.Fatalf("expected %s to be package asset indexed", format)
		}
	}
	if packageAssetIndexFormat("maven2") {
		t.Fatalf("did not expect maven2 to be package asset indexed")
	}
}
