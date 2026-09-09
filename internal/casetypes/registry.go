package casetypes

import (
	"encoding/json"
	"errors"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const (
	TypeOpenAIChat   domain.CaseType = "openai-chat"
	TypeSeedance     domain.CaseType = "seedance"
	TypeWanVideo     domain.CaseType = "wan-video"
	TypeMiniMaxVideo domain.CaseType = "minimax-video"
)

type SchedulingOwner string

const SchedulingOwnerPlan SchedulingOwner = "plan"

type Descriptor struct {
	Type               domain.CaseType           `json:"type"`
	TypeVersion        uint32                    `json:"type_version"`
	Label              string                    `json:"label"`
	Category           string                    `json:"category"`
	SchedulingOwner    SchedulingOwner           `json:"scheduling_owner"`
	SupportedProtocols []domain.Protocol         `json:"supported_protocols"`
	Creatable          bool                      `json:"creatable"`
	DefaultSpec        json.RawMessage           `json:"default_spec"`
	ObservationMetrics []testspec.Metric         `json:"observation_metrics"`
	Settings           map[string]testspec.Input `json:"settings"`
}

type Registry struct{ protocols *protocols.Registry }

func NewBuiltinRegistry() (*Registry, error) {
	registry := &Registry{protocols: protocols.NewRegistry()}
	for _, descriptor := range registry.protocols.Descriptors() {
		if err := registry.protocols.Validate(descriptor.ID, descriptor.DefaultSpec); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func MustBuiltinRegistry() *Registry {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry *Registry) Descriptors() []Descriptor {
	result := []Descriptor{}
	if registry == nil {
		return result
	}
	for _, module := range registry.protocols.Descriptors() {
		raw, _ := json.Marshal(module.DefaultSpec)
		result = append(result, Descriptor{
			Type: domain.CaseType(module.ID), TypeVersion: 1, Label: module.Label,
			Category: "protocol", SchedulingOwner: SchedulingOwnerPlan,
			SupportedProtocols: []domain.Protocol{domain.Protocol(module.ID)},
			Creatable:          true, DefaultSpec: raw, ObservationMetrics: module.Metrics, Settings: module.Settings,
		})
	}
	return result
}

func (registry *Registry) Descriptor(caseType domain.CaseType, version uint32) (Descriptor, bool) {
	if version != 1 {
		return Descriptor{}, false
	}
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Type == caseType {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func (registry *Registry) Validate(protocol domain.Protocol, definition domain.TestCaseDefinition) error {
	if registry == nil {
		return errors.New("case type registry is unavailable")
	}
	if err := definition.Validate(); err != nil {
		return err
	}
	if definition.TypeVersion != 1 {
		return errors.New("unsupported case type format; use the current protocol contract")
	}
	if string(definition.Type) != string(protocol) {
		return errors.New("case type must equal its protocol")
	}
	spec, err := testspec.Decode(definition.Spec)
	if err != nil {
		return err
	}
	return registry.protocols.Validate(string(protocol), spec)
}
