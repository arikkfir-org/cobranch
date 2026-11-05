// Command tokens writes the Console's tokens.css from tokens.json, the design system's tokens as figma-export.js
// exports them from the Figma file: every variable under the CSS name its codeSyntax gives it, each mode but the first
// (Dark) in the blocks that the system's theme or the person's choice turns on, every text style as a font shorthand
// with its letter-spacing, and every effect style as a box-shadow. The Figma file is the source of truth, so tokens.css
// is never edited by hand: go generate in web/console/tools runs this, and CI fails when tokens.css differs from what
// it writes.
//
//	go run ./tokens tokens.json ../public/assets/tokens.css
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// export is tokens.json, as figma-export.js returns it.
type export struct {
	File         string        `json:"file"`
	Collections  []collection  `json:"collections"`
	TextStyles   []textStyle   `json:"textStyles"`
	EffectStyles []effectStyle `json:"effectStyles"`
}

type collection struct {
	Name      string     `json:"name"`
	Modes     []string   `json:"modes"`
	Variables []variable `json:"variables"`
}

type variable struct {
	Name        string                     `json:"name"`
	CSS         string                     `json:"css"`
	Type        string                     `json:"type"`
	Description string                     `json:"description"`
	Values      map[string]json.RawMessage `json:"values"`
}

type textStyle struct {
	Name          string          `json:"name"`
	Family        string          `json:"family"`
	Style         string          `json:"style"`
	Size          float64         `json:"size"`
	LineHeight    json.RawMessage `json:"lineHeight"`
	LetterSpacing json.RawMessage `json:"letterSpacing"`
	TextCase      string          `json:"textCase"`
}

type effectStyle struct {
	Name    string   `json:"name"`
	Effects []effect `json:"effects"`
}

type effect struct {
	Type   string          `json:"type"`
	X      float64         `json:"x"`
	Y      float64         `json:"y"`
	Radius float64         `json:"radius"`
	Spread float64         `json:"spread"`
	Color  json.RawMessage `json:"color"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tokens <tokens.json> <tokens.css>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run reads the export at in and writes its stylesheet to out.
func run(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return fmt.Errorf("reading the export %s: %w", in, err)
	}
	var e export
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("parsing the export %s: %w", in, err)
	}
	css, err := stylesheet(e)
	if err != nil {
		return fmt.Errorf("writing the stylesheet of %s: %w", in, err)
	}
	if err := os.WriteFile(out, []byte(css), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	return nil
}
