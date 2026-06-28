package registry

import (
	"context"
	"fmt"
	"sort"

	"github.com/regclient/regclient"
	"github.com/regclient/regclient/config"
	"github.com/regclient/regclient/types/descriptor"
	"github.com/regclient/regclient/types/manifest"
	"github.com/regclient/regclient/types/ref"
)

type Config struct {
	RegistryHost      string
	Username          string
	Password          string
	PlainHTTP         bool
	RequestsPerSecond float64
	MaxConcurrent     int64
}

type Client struct {
	rc *regclient.RegClient
}

type Descriptor struct {
	MediaType string
	Digest    string
	Size      int64
	Platform  string
}

type ManifestSummary struct {
	MediaType string
	Digest    string
	Size      int64
	IsList    bool
	Children  []Descriptor
	Config    *Descriptor
	Layers    []Descriptor
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.RegistryHost == "" {
		return nil, fmt.Errorf("registry host is required")
	}

	host := config.Host{
		Name:          cfg.RegistryHost,
		Hostname:      cfg.RegistryHost,
		User:          cfg.Username,
		Pass:          cfg.Password,
		ReqPerSec:     cfg.RequestsPerSecond,
		ReqConcurrent: cfg.MaxConcurrent,
	}
	if cfg.PlainHTTP {
		host.TLS = config.TLSDisabled
	} else {
		host.TLS = config.TLSEnabled
	}

	return &Client{
		rc: regclient.New(regclient.WithConfigHost(host)),
	}, nil
}

func (c *Client) ListTags(ctx context.Context, image string) ([]string, error) {
	r, err := ref.New(image)
	if err != nil {
		return nil, fmt.Errorf("parse image ref %q: %w", image, err)
	}

	tags, err := c.rc.TagList(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("list tags for %q: %w", image, err)
	}

	out := append([]string(nil), tags.Tags...)
	sort.Strings(out)
	return out, nil
}

func (c *Client) ManifestSummary(ctx context.Context, image, tag string) (ManifestSummary, error) {
	r, err := ref.New(image + ":" + tag)
	if err != nil {
		return ManifestSummary{}, fmt.Errorf("parse image ref %q:%q: %w", image, tag, err)
	}

	m, err := c.rc.ManifestGet(ctx, r)
	if err != nil {
		return ManifestSummary{}, fmt.Errorf("get manifest for %q:%q: %w", image, tag, err)
	}

	desc := m.GetDescriptor()
	out := ManifestSummary{
		MediaType: desc.MediaType,
		Digest:    desc.Digest.String(),
		Size:      desc.Size,
		IsList:    m.IsList(),
	}

	if idx, ok := m.(manifest.Indexer); ok {
		children, err := idx.GetManifestList()
		if err != nil {
			return ManifestSummary{}, fmt.Errorf("get child manifests for %q:%q: %w", image, tag, err)
		}
		out.Children = descriptors(children)
	}

	if img, ok := m.(manifest.Imager); ok {
		cfg, err := img.GetConfig()
		if err != nil {
			return ManifestSummary{}, fmt.Errorf("get config descriptor for %q:%q: %w", image, tag, err)
		}
		cfgDesc := descriptorSummary(cfg)
		out.Config = &cfgDesc

		layers, err := img.GetLayers()
		if err != nil {
			return ManifestSummary{}, fmt.Errorf("get layer descriptors for %q:%q: %w", image, tag, err)
		}
		out.Layers = descriptors(layers)
	}

	return out, nil
}

func descriptors(in []descriptor.Descriptor) []Descriptor {
	out := make([]Descriptor, 0, len(in))
	for _, d := range in {
		out = append(out, descriptorSummary(d))
	}
	return out
}

func descriptorSummary(d descriptor.Descriptor) Descriptor {
	out := Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest.String(),
		Size:      d.Size,
	}
	if d.Platform != nil {
		out.Platform = d.Platform.String()
	}
	return out
}
