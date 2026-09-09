// Package protocols explicitly assembles protocol modules and owns their shared HTTP boundary.
package protocols

import (
	"errors"
	"sort"

	"github.com/894x/llm-test-studio/internal/protocols/minimaxvideo"
	"github.com/894x/llm-test-studio/internal/protocols/openaichat"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/protocols/seedance"
	"github.com/894x/llm-test-studio/internal/protocols/wanvideo"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type Registry struct{ modules map[string]runtime.Module }

func NewRegistry() *Registry {
	registry := &Registry{modules: map[string]runtime.Module{}}
	// Composition is explicit and local; protocol packages do not register via init.
	for _, module := range []runtime.Module{openaichat.New(), seedance.New(), wanvideo.New(), minimaxvideo.New()} {
		registry.modules[module.Descriptor().ID] = module
	}
	return registry
}

func (registry *Registry) Register(module runtime.Module) error {
	if registry == nil || module == nil {
		return errors.New("protocol module is required")
	}
	id := module.Descriptor().ID
	if id == "" {
		return errors.New("protocol identity is required")
	}
	if _, exists := registry.modules[id]; exists {
		return errors.New("protocol is already registered")
	}
	if err := module.Validate(module.Descriptor().DefaultSpec); err != nil {
		return err
	}
	registry.modules[id] = module
	return nil
}

func (registry *Registry) Descriptors() []runtime.Descriptor {
	result := []runtime.Descriptor{}
	if registry == nil {
		return result
	}
	for _, module := range registry.modules {
		result = append(result, module.Descriptor())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (registry *Registry) Validate(protocol string, spec testspec.Spec) error {
	module, ok := registry.lookup(protocol)
	if !ok {
		return errors.New("protocol is not registered")
	}
	return module.Validate(spec)
}

func (registry *Registry) lookup(protocol string) (runtime.Module, bool) {
	if registry == nil {
		return nil, false
	}
	module, exists := registry.modules[protocol]
	return module, exists
}
