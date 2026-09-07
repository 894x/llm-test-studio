// Package jsonpointer resolves JSON Pointer paths shared by case assertions and
// editable task parameters.
package jsonpointer

import (
	"strconv"
	"strings"
)

func Parse(pointer string) ([]string, bool) {
	if pointer == "" {
		return nil, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	tokens := strings.Split(pointer[1:], "/")
	for i, token := range tokens {
		for j := 0; j < len(token); j++ {
			if token[j] == '~' {
				if j+1 >= len(token) || (token[j+1] != '0' && token[j+1] != '1') {
					return nil, false
				}
				j++
			}
		}
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, true
}

func Lookup(root any, pointer string) (any, bool) {
	tokens, valid := Parse(pointer)
	if !valid {
		return nil, false
	}
	current := root
	for _, token := range tokens {
		var found bool
		current, found = child(current, token)
		if !found {
			return nil, false
		}
	}
	return current, true
}

// Replace changes one existing field; it never creates paths or replaces the
// root object. Traversal uses the same rules as Lookup.
func Replace(root any, pointer string, replacement any) bool {
	tokens, valid := Parse(pointer)
	if !valid || len(tokens) == 0 {
		return false
	}
	parent := root
	for _, token := range tokens[:len(tokens)-1] {
		var found bool
		parent, found = child(parent, token)
		if !found {
			return false
		}
	}
	last := tokens[len(tokens)-1]
	if _, found := child(parent, last); !found {
		return false
	}
	switch value := parent.(type) {
	case map[string]any:
		value[last] = replacement
	case []any:
		index, _ := strconv.Atoi(last) // child already validated this index.
		value[index] = replacement
	}
	return true
}

func child(parent any, token string) (any, bool) {
	switch value := parent.(type) {
	case map[string]any:
		result, found := value[token]
		return result, found
	case []any:
		if token == "" || (len(token) > 1 && token[0] == '0') {
			return nil, false
		}
		for _, digit := range token {
			if digit < '0' || digit > '9' {
				return nil, false
			}
		}
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 || index >= len(value) {
			return nil, false
		}
		return value[index], true
	default:
		return nil, false
	}
}
