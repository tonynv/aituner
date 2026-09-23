// Package canirun is a client for the public canirun.ai JSON API, documented in
// https://github.com/midudev/canirun.ai (README "API"). The apex domain answers 307 to www, so we call www.
package canirun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tonynv/aituner/internal/httpx"
)

const Base = "https://www.canirun.ai"

type Client struct {
	Base string
	HTTP *httpx.Client
}

func New() *Client {
	return &Client{Base: Base, HTTP: httpx.New("www.canirun.ai", "canirun.ai")}
}

type Hardware struct {
	CPU   *CPU `json:"cpu,omitempty"`
	RAMGb int  `json:"ramGb"`
	GPU   *GPU `json:"gpu,omitempty"`
}

type CPU struct {
	Name    string `json:"name"`
	Cores   int    `json:"cores,omitempty"`
	Threads int    `json:"threads,omitempty"`
}

type GPU struct {
	Name string `json:"name"`
}

type Recommendation struct {
	ModelID        string   `json:"modelId"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	Family         string   `json:"family"`
	ParamsBillions float64  `json:"paramsBillions"`
	Quantization   string   `json:"quantization"`
	Status         string   `json:"status"`
	Grade          string   `json:"grade"`
	Score          float64  `json:"score"`
	EstTPS         float64  `json:"estimatedTokensPerSecond"`
	VRAMRequiredGb float64  `json:"vramRequiredGb"`
	DiskSizeGb     float64  `json:"diskSizeGb"`
	URL            string   `json:"url"`
	UseCase        []string `json:"useCase"`
}

type DetectedHardware struct {
	Kind                string  `json:"kind"`
	Device              string  `json:"device"`
	RAMGb               float64 `json:"ramGb"`
	MemoryBandwidthGbps float64 `json:"memoryBandwidthGbps"`
}

type RecommendResponse struct {
	Hardware        DetectedHardware `json:"hardware"`
	Count           int              `json:"count"`
	Recommendations []Recommendation `json:"recommendations"`
}

type Model struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	Family         string   `json:"family"`
	ParamsBillions float64  `json:"paramsBillions"`
	Architecture   string   `json:"architecture"` // dense | moe
	ReleaseDate    string   `json:"releaseDate"`
	ContextLength  int      `json:"contextLength"`
	UseCase        []string `json:"useCase"`
	URL            string   `json:"url"`
	License        string   `json:"license"`
}

// Recommend ranks compatible models for a hardware profile. limit is 1-25 per the API.
func (c *Client) Recommend(ctx context.Context, hw Hardware, useCase string, limit int) (*RecommendResponse, error) {
	if limit < 1 || limit > 25 {
		return nil, errors.New("limit must be 1..25")
	}
	body := map[string]any{"hardware": hw, "limit": limit}
	if useCase != "" {
		body["useCase"] = useCase
	}
	b, _ := json.Marshal(body)
	var out RecommendResponse
	if err := c.HTTP.JSON(ctx, "POST", c.Base+"/api/recommend", bytes.NewReader(b), &out); err != nil {
		return nil, err
	}
	for i, r := range out.Recommendations {
		if r.ModelID == "" || r.Quantization == "" || r.VRAMRequiredGb <= 0 {
			return nil, fmt.Errorf("canirun.ai: malformed recommendation #%d", i)
		}
	}
	return &out, nil
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var out struct {
		Models []Model `json:"models"`
	}
	// the endpoint returns {"models":[...]}; verified live
	if err := c.HTTP.JSON(ctx, "GET", c.Base+"/api/models", nil, &out); err != nil {
		return nil, err
	}
	if len(out.Models) == 0 {
		return nil, errors.New("canirun.ai: empty model catalog")
	}
	return out.Models, nil
}
