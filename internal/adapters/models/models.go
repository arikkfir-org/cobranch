// Package models holds the model providers an agent's harness speaks to, each an adapter of its own behind one model
// interface, and none called unless an organization added its provider.
package models

// Providers names the model providers Cobranch has adapters for: those an organization may add, by the name the API
// and the Console show.
func Providers() []string {
	return []string{"anthropic", "deepseek", "google", "openai", "xai"}
}
