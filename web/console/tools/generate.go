// Package tools holds the Console's tools: tokens, which writes the Console's tokens.css from the design system's
// tokens.json, the export of the Figma file (README.md).
package tools

//go:generate go run ./tokens tokens.json ../public/assets/tokens.css
