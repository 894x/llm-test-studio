package testspec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

func validateTemplate(raw json.RawMessage, inputs map[string]Input, steps map[string]bool) error {
	value, err := decodeValue(raw)
	if err != nil {
		return errors.New("request.body must contain a bounded JSON object")
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return errors.New("request.body must be a JSON object")
	}
	if _, exists := object["model"]; exists {
		return errors.New("request.body.model is owned by the run binding; remove it from the case")
	}
	if _, exists := object["$input"]; exists {
		return errors.New("the complete request body cannot be replaced by an input")
	}
	return validateNode(value, inputs, steps, 0)
}

func validateNode(value any, inputs map[string]Input, steps map[string]bool, depth int) error {
	if depth > 64 {
		return errors.New("request template exceeds 64 nesting levels")
	}
	switch current := value.(type) {
	case []any:
		for _, nested := range current {
			if err := validateNode(nested, inputs, steps, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if name, exists := current["$input"]; exists {
			inputName, ok := name.(string)
			if !ok || len(current) != 1 {
				return errors.New("$input requires only a declared input name")
			}
			if _, declared := inputs[inputName]; !declared {
				return errors.New("template references an undeclared input")
			}
			return nil
		}
		if step, exists := current["$response"]; exists {
			stepName, ok := step.(string)
			pointer, pointerOK := current["pointer"].(string)
			_, validPointer := jsonpointer.Parse(pointer)
			if !ok || !pointerOK || !steps[stepName] || !validPointer || len(current) != 2 {
				return errors.New("$response requires a preceding step and valid pointer")
			}
			return nil
		}
		if generator, exists := current["$generate"]; exists {
			name, ok := generator.(string)
			if !ok {
				return errors.New("$generate must name a registered generator")
			}
			switch name {
			case "concat":
				if _, ok := current["parts"].([]any); !ok || len(current) != 2 {
					return errors.New("concat requires parts array")
				}
			case "repeat":
				if _, ok := current["count"]; !ok || len(current) != 3 {
					return errors.New("repeat requires text and count")
				}
			case "repeat_text", "random_text":
				if _, exists := current["length"]; !exists {
					return errors.New("text generator requires length")
				}
				field := "text"
				if name == "random_text" {
					field = "alphabet"
				}
				if _, exists := current[field]; !exists {
					return errors.New("text generator requires its character source")
				}
				if len(current) != 3 {
					return errors.New("text generator has unsupported settings")
				}
			default:
				return errors.New("unregistered generator")
			}
		}
		for name, nested := range current {
			if strings.HasPrefix(name, "$") && name != "$generate" {
				return errors.New("unknown template directive")
			}
			if err := validateNode(nested, inputs, steps, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func RequiresRandom(spec Spec) bool {
	requests := []Request{spec.Request}
	if spec.Workflow != nil {
		for _, step := range spec.Workflow.Steps {
			requests = append(requests, step.Request)
		}
	}
	for _, request := range requests {
		value, _ := decodeValue(request.Body)
		if randomNode(value) {
			return true
		}
	}
	return false
}

func randomNode(value any) bool {
	switch current := value.(type) {
	case []any:
		for _, nested := range current {
			if randomNode(nested) {
				return true
			}
		}
	case map[string]any:
		if current["$generate"] == "random_text" {
			return true
		}
		for _, nested := range current {
			if randomNode(nested) {
				return true
			}
		}
	}
	return false
}

func Generate(spec Spec, values map[string]json.RawMessage, random RandomContext) (json.RawMessage, error) {
	resolved, err := ValidateInputs(spec.Inputs, values)
	if err != nil {
		return nil, err
	}
	return Render(spec.Request, resolved, random, map[string]json.RawMessage{})
}

// Render evaluates explicit template nodes. Strings never undergo interpolation.
func Render(
	request Request,
	values map[string]json.RawMessage,
	random RandomContext,
	responses map[string]json.RawMessage,
) (json.RawMessage, error) {
	value, err := decodeValue(request.Body)
	if err != nil {
		return nil, err
	}
	state := generatorState{values: values, random: random, responses: responses}
	generated, err := state.render(value, "body", 0)
	if err != nil {
		return nil, err
	}
	object, ok := generated.(map[string]any)
	if !ok {
		return nil, errors.New("generated request body must be an object")
	}
	if _, exists := object["model"]; exists {
		return nil, errors.New("generated model conflicts with run binding")
	}
	raw, err := json.Marshal(generated)
	if err != nil || len(raw) > MaxDocumentBytes {
		return nil, errors.New("generated request exceeds the 4 MiB limit")
	}
	return raw, nil
}

type generatorState struct {
	values         map[string]json.RawMessage
	random         RandomContext
	responses      map[string]json.RawMessage
	generatedBytes int
}

func (state *generatorState) render(value any, path string, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("generator nesting exceeds limit")
	}
	switch current := value.(type) {
	case []any:
		result := make([]any, len(current))
		for index, nested := range current {
			generated, err := state.render(nested, path+"/"+strconv.Itoa(index), depth+1)
			if err != nil {
				return nil, err
			}
			result[index] = generated
		}
		return result, nil
	case map[string]any:
		if name, ok := current["$input"].(string); ok {
			raw, exists := state.values[name]
			if !exists {
				return nil, fmt.Errorf("referenced input %s has no value", name)
			}
			state.generatedBytes += len(raw)
			if state.generatedBytes > MaxDocumentBytes {
				return nil, errors.New("expanded inputs exceed the request budget")
			}
			return decodeValue(raw)
		}
		if step, ok := current["$response"].(string); ok {
			state.generatedBytes += len(state.responses[step])
			if state.generatedBytes > MaxDocumentBytes {
				return nil, errors.New("expanded response references exceed the request budget")
			}
			root, err := decodeValue(state.responses[step])
			if err != nil {
				return nil, errors.New("referenced step response is unavailable")
			}
			pointer, _ := current["pointer"].(string)
			found, exists := jsonpointer.Lookup(root, pointer)
			if !exists {
				return nil, errors.New("referenced step response field is missing")
			}
			return found, nil
		}
		result := make(map[string]any, len(current))
		names := make([]string, 0, len(current))
		for name := range current {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			generated, err := state.render(current[name], path+"/"+name, depth+1)
			if err != nil {
				return nil, err
			}
			result[name] = generated
		}
		if generator, ok := result["$generate"].(string); ok {
			if generator == "concat" || generator == "repeat" {
				return state.combine(generator, result)
			}
			return state.text(generator, result, path)
		}
		return result, nil
	default:
		return value, nil
	}
}

func (state *generatorState) combine(generator string, node map[string]any) (any, error) {
	if generator == "repeat" {
		text, ok := node["text"].(string)
		count, number := numeric(node["count"])
		if !ok || !number || count < 0 || count > MaxDocumentBytes || math.Trunc(count) != count {
			return nil, errors.New("repeat requires text and bounded nonnegative count")
		}
		if float64(len(text))*count > MaxDocumentBytes {
			return nil, errors.New("repeat output exceeds request budget")
		}
		state.generatedBytes += len(text) * int(count)
		if state.generatedBytes > MaxDocumentBytes {
			return nil, errors.New("generators exceed total request budget")
		}
		return strings.Repeat(text, int(count)), nil
	}
	parts, ok := node["parts"].([]any)
	if !ok {
		return nil, errors.New("concat parts must be an array")
	}
	var result strings.Builder
	for _, part := range parts {
		text, ok := part.(string)
		if !ok {
			return nil, errors.New("concat parts must resolve to strings")
		}
		if result.Len()+len(text) > MaxDocumentBytes {
			return nil, errors.New("concat output exceeds request budget")
		}
		result.WriteString(text)
	}
	return result.String(), nil
}

func (state *generatorState) text(generator string, node map[string]any, path string) (any, error) {
	number, ok := numeric(node["length"])
	if !ok || number < 0 || number > MaxDocumentBytes/4 || math.Trunc(number) != number {
		return nil, errors.New("generator length must be a bounded non-negative integer")
	}
	length := int(number)
	field := "text"
	if generator == "random_text" {
		field = "alphabet"
	}
	text, ok := node[field].(string)
	if !ok || text == "" {
		return nil, errors.New("generator character source must be a non-empty string")
	}
	characters := []rune(text)
	state.generatedBytes += length * 4
	if state.generatedBytes > MaxDocumentBytes {
		return nil, errors.New("generated text exceeds the total request budget")
	}
	result := make([]rune, length)
	seed := deriveSeed(state.random, path)
	random := rand.New(rand.NewChaCha8(seed))
	for index := range result {
		characterIndex := index % len(characters)
		if generator == "random_text" {
			characterIndex = random.IntN(len(characters))
		}
		result[index] = characters[characterIndex]
	}
	return string(result), nil
}

func deriveSeed(context RandomContext, path string) [32]byte {
	hash := sha256.New()
	_ = binary.Write(hash, binary.LittleEndian, context.Seed)
	_ = binary.Write(hash, binary.LittleEndian, context.Iteration)
	for _, part := range []string{context.ExecutionItemID, context.MemberID, path} {
		_ = binary.Write(hash, binary.LittleEndian, uint64(len(part)))
		_, _ = hash.Write([]byte(part))
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}
