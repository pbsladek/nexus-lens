package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pbsladek/nexus-lens/internal/upstream"
)

type Client struct {
	baseURL    *url.URL
	username   string
	password   string
	httpClient *http.Client
}

type Repository struct {
	Name       string         `json:"name"`
	Format     string         `json:"format"`
	Type       string         `json:"type"`
	URL        string         `json:"url"`
	Online     *bool          `json:"online,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Asset struct {
	ID             string         `json:"id"`
	Repository     string         `json:"repository"`
	Format         string         `json:"format"`
	Path           string         `json:"path"`
	DownloadURL    string         `json:"downloadUrl"`
	ContentType    string         `json:"contentType"`
	FileSize       int64          `json:"fileSize"`
	LastModified   string         `json:"lastModified"`
	LastDownloaded string         `json:"lastDownloaded"`
	BlobCreated    string         `json:"blobCreated"`
	BlobStoreName  string         `json:"blobStoreName"`
	Checksum       AssetChecksum  `json:"checksum"`
	Raw            map[string]any `json:"-"`
}

type AssetChecksum struct {
	SHA1   string `json:"sha1"`
	SHA256 string `json:"sha256"`
}

type AssetsPage struct {
	Items             []Asset `json:"items"`
	ContinuationToken string  `json:"continuationToken"`
}

func NewClient(baseURL, username, password string) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("nexus base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse nexus base URL: %w", err)
	}
	return &Client{
		baseURL:  parsed,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (c *Client) ListRepositories(ctx context.Context) ([]Repository, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/service/rest/v1/repositories")
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list nexus repositories: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstream.NewHTTPError("list nexus repositories", resp)
	}
	var repos []Repository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("decode nexus repositories: %w", err)
	}
	return repos, nil
}

func (c *Client) ListAssets(ctx context.Context, repository, continuationToken string) (AssetsPage, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/service/rest/v1/assets")
	if err != nil {
		return AssetsPage{}, err
	}
	query := req.URL.Query()
	query.Set("repository", repository)
	if continuationToken != "" {
		query.Set("continuationToken", continuationToken)
	}
	req.URL.RawQuery = query.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AssetsPage{}, fmt.Errorf("list nexus assets for repository %q: %w", repository, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AssetsPage{}, upstream.NewHTTPError(fmt.Sprintf("list nexus assets for repository %q", repository), resp)
	}
	var page AssetsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return AssetsPage{}, fmt.Errorf("decode nexus assets for repository %q: %w", repository, err)
	}
	return page, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string) (*http.Request, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}
