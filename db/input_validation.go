package db

import (
	"fmt"
	"strings"
)

// InputError is shared by REST and platform tools; DB constraints remain in place.
type InputError struct {
	Field   string
	Allowed []string
}

func (e *InputError) Error() string {
	return fmt.Sprintf("%s 取值无效，可用值: %s", e.Field, strings.Join(e.Allowed, ", "))
}
func normalizeEnum(field, value string, allowed ...string) (string, error) {
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", &InputError{Field: field, Allowed: allowed}
}
func NormalizeLLMFormat(value string) (string, error) {
	return normalizeEnum("format", value, "openai", "anthropic", "openai-responses")
}
func NormalizeMCPTransport(value string) (string, error) {
	return normalizeEnum("transport", value, "stdio", "http", "sse")
}
