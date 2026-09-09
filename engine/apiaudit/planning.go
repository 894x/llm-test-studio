package apiaudit

import (
	"fmt"
	"strings"
)

const DefaultSeedanceModel = "doubao-seedance-2-0-260128"

var DefaultSeedanceModels = []string{
	DefaultSeedanceModel,
	"doubao-seedance-2-0-fast-260128",
	"doubao-seedance-2-0-mini-260615",
}

func ExpandRuns(config RunConfig, cases []CaseDefinition) ([]PlannedRun, error) {
	models := append([]string(nil), config.Models...)
	if len(models) == 0 {
		model := strings.TrimSpace(config.Model)
		if model == "" && config.Suite == "seedance" {
			model = DefaultSeedanceModel
		}
		if model == "" {
			return nil, fmt.Errorf("model is required")
		}
		models = []string{model}
	}
	for _, model := range models {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("model list contains an empty value")
		}
	}

	runs := make([]PlannedRun, 0, len(cases)*len(models))
	for _, definition := range cases {
		for _, model := range models {
			resultID := definition.ID
			if len(models) > 1 {
				resultID += "@" + model
			}
			runs = append(runs, PlannedRun{Case: definition, Model: model, ResultID: resultID})
		}
	}
	return runs, nil
}
