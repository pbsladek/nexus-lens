package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pbsladek/nexus-lens/internal/upstream"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

type RepositoryClient struct {
	baseURL    *url.URL
	username   string
	password   string
	httpClient *http.Client
}

type ManifestDocument struct {
	Digest    string
	MediaType string
	Size      int64
	Config    *Descriptor
	Layers    []Descriptor
	Children  []Descriptor
}

type rawManifestDocument struct {
	MediaType string          `json:"mediaType"`
	Config    *rawDescriptor  `json:"config"`
	Layers    []rawDescriptor `json:"layers"`
	Manifests []rawDescriptor `json:"manifests"`
}

type rawDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  any    `json:"platform,omitempty"`
}

func NewRepositoryClient(repositoryURL, username, password string) (*RepositoryClient, error) {
	if repositoryURL == "" {
		return nil, fmt.Errorf("repository URL is required")
	}
	parsed, err := url.Parse(repositoryURL)
	if err != nil {
		return nil, fmt.Errorf("parse repository URL: %w", err)
	}
	return &RepositoryClient{
		baseURL:  parsed,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (c *RepositoryClient) FetchManifest(ctx context.Context, image, reference string) (ManifestDocument, error) {
	if image == "" || reference == "" {
		return ManifestDocument{}, fmt.Errorf("image and reference are required")
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + "/v2/" + image + "/manifests/" + reference
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ManifestDocument{}, err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	req.Header.Set("Accept", manifestAccept)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ManifestDocument{}, fmt.Errorf("fetch manifest %s:%s: %w", image, reference, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ManifestDocument{}, upstream.NewHTTPError(fmt.Sprintf("fetch manifest %s:%s", image, reference), resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ManifestDocument{}, fmt.Errorf("read manifest %s:%s: %w", image, reference, err)
	}
	var raw rawManifestDocument
	if err := json.Unmarshal(body, &raw); err != nil {
		return ManifestDocument{}, fmt.Errorf("decode manifest %s:%s: %w", image, reference, err)
	}

	mediaType := raw.MediaType
	if mediaType == "" {
		mediaType = resp.Header.Get("Content-Type")
	}
	out := ManifestDocument{
		Digest:    resp.Header.Get("Docker-Content-Digest"),
		MediaType: mediaType,
		Size:      int64(len(body)),
		Layers:    rawDescriptors(raw.Layers),
		Children:  rawDescriptors(raw.Manifests),
	}
	if raw.Config != nil {
		config := rawDescriptorSummary(*raw.Config)
		out.Config = &config
	}
	return out, nil
}

func rawDescriptors(in []rawDescriptor) []Descriptor {
	out := make([]Descriptor, 0, len(in))
	for _, desc := range in {
		out = append(out, rawDescriptorSummary(desc))
	}
	return out
}

func rawDescriptorSummary(in rawDescriptor) Descriptor {
	return Descriptor{
		MediaType: in.MediaType,
		Digest:    in.Digest,
		Size:      in.Size,
	}
}
