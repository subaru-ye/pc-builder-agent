package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

// Resolve and pin both roles before creating clients or issuing any request.
func fixedConfigs(path string) (map[modelprovider.Role]modelprovider.Config, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var pin struct {
		Models map[string]map[string]any `json:"models"`
	}
	if err = json.Unmarshal(raw, &pin); err != nil {
		return nil, nil, err
	}
	dotenv.Load(".env")
	configs := map[modelprovider.Role]modelprovider.Config{}
	redacted := map[string]any{}
	for _, role := range []modelprovider.Role{modelprovider.RoleScreening, modelprovider.RoleBuilder} {
		cfg, err := modelprovider.Load(role)
		if err != nil {
			return nil, nil, err
		}
		if cfg.MaxRetries != 0 || len(cfg.ModelChain) != 0 {
			return nil, nil, fmt.Errorf("live evaluation requires zero retries and no model chain")
		}
		description := cfg.Redacted()
		description["model_chain"] = []string{}
		description["timeout"], description["dimensions"] = cfg.Timeout.String(), cfg.Dimensions
		encoded, _ := json.Marshal(description)
		var normalized map[string]any
		_ = json.Unmarshal(encoded, &normalized)
		if !reflect.DeepEqual(normalized, pin.Models[string(role)]) {
			return nil, nil, fmt.Errorf("%s config differs from pinned model settings", role)
		}
		configs[role], redacted[string(role)] = cfg, description
	}
	return configs, redacted, nil
}

func liveModels(ctx context.Context, configs map[modelprovider.Role]modelprovider.Config, limit int) (planningeval.Models, error) {
	m := planningeval.Models{MaxCalls: limit}
	var err error
	m.Screening, err = modelprovider.NewChat(ctx, configs[modelprovider.RoleScreening], "planning-eval-screening")
	if err != nil {
		return m, err
	}
	m.Builder, err = modelprovider.NewChat(ctx, configs[modelprovider.RoleBuilder], "planning-eval-builder")
	return m, err
}
