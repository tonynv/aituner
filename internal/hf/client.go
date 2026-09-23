// Package hf reads public model metadata from the Hugging Face Hub API (https://huggingface.co/docs/hub/api).
package hf

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/tonynv/aituner/internal/httpx"
)

const Base = "https://huggingface.co"

type Client struct {
	Base string
	HTTP *httpx.Client
}

func New() *Client { return &Client{Base: Base, HTTP: httpx.New("huggingface.co")} }

type Repo struct {
	ID           string   `json:"id"`
	Downloads    int      `json:"downloads"`
	Likes        int      `json:"likes"`
	LastModified string   `json:"lastModified"`
	Tags         []string `json:"tags"`
	PipelineTag  string   `json:"pipeline_tag"`
	Gated        any      `json:"gated"`
}

func (r Repo) Author() string { return strings.SplitN(r.ID, "/", 2)[0] }
func (r Repo) Name() string   { return path.Base(r.ID) }

// License returns the license tag value (e.g. "apache-2.0") or "".
func (r Repo) License() string {
	for _, t := range r.Tags {
		if v, ok := strings.CutPrefix(t, "license:"); ok {
			return v
		}
	}
	return ""
}

func (r Repo) IsGated() bool {
	switch v := r.Gated.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false"
	}
	return false
}

type SearchOpts struct {
	Author string
	Search string
	Filter string
	Limit  int
}

func (c *Client) Search(ctx context.Context, o SearchOpts) ([]Repo, error) {
	q := url.Values{}
	if o.Author != "" {
		q.Set("author", o.Author)
	}
	if o.Search != "" {
		q.Set("search", o.Search)
	}
	if o.Filter != "" {
		q.Set("filter", o.Filter)
	}
	if o.Limit == 0 {
		o.Limit = 20
	}
	q.Set("limit", fmt.Sprint(o.Limit))
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	var out []Repo
	err := c.HTTP.JSON(ctx, "GET", c.Base+"/api/models?"+q.Encode(), nil, &out)
	return out, err
}

type Sibling struct {
	Name string `json:"rfilename"`
	Size int64  `json:"size"`
}

type Info struct {
	Repo
	Siblings []Sibling `json:"siblings"`
	Config   struct {
		ModelType string `json:"model_type"`
	} `json:"config"`
}

// SafetensorsBytes sums *.safetensors file sizes: the bytes that must be resident to run the model.
func (i Info) SafetensorsBytes() int64 {
	var n int64
	for _, s := range i.Siblings {
		if strings.HasSuffix(s.Name, ".safetensors") {
			n += s.Size
		}
	}
	return n
}

// PickleOnly is true when the repo has weight files but no safetensors (unsafe to load).
func (i Info) PickleOnly() bool {
	var st, pk bool
	for _, s := range i.Siblings {
		switch {
		case strings.HasSuffix(s.Name, ".safetensors"):
			st = true
		case strings.HasSuffix(s.Name, ".bin"), strings.HasSuffix(s.Name, ".pt"), strings.HasSuffix(s.Name, ".pth"), strings.HasSuffix(s.Name, ".pkl"), strings.HasSuffix(s.Name, ".ckpt"):
			pk = true
		}
	}
	return pk && !st
}

func (c *Client) Info(ctx context.Context, id string) (Info, error) {
	var out Info
	err := c.HTTP.JSON(ctx, "GET", c.Base+"/api/models/"+id+"?blobs=true", nil, &out)
	return out, err
}
