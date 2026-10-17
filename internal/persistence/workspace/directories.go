package workspace

import "strings"

func CaseDirectory(key string) string {
	var result strings.Builder
	for _, character := range key {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			result.WriteRune(character)
		default:
			result.WriteByte('-')
		}
	}
	value := strings.Trim(result.String(), "-.")
	if value == "" {
		return "case"
	}
	return value
}

func SuiteDirectory(key string) string {
	return CaseDirectory(key)
}
