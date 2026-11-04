package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// textTokens are the text styles' custom properties: --text-<name> a font shorthand, in the style's typeface, and
// --text-<name>-tracking its letter-spacing, 0px where it has none, so that a rule that sets both never inherits
// another style's.
func textTokens(styles []textStyle, fonts map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString("\n  /* Text styles */\n")
	for _, s := range styles {
		weight, ok := weights[s.Style]
		if !ok {
			return "", fmt.Errorf("text style %s is in style %s, which has no CSS weight here", s.Name, s.Style)
		}
		if s.TextCase != "" {
			return "", fmt.Errorf("text style %s sets text case %s, which tokens.css doesn't carry", s.Name, s.TextCase)
		}
		line, err := length(s.LineHeight)
		if err != nil {
			return "", fmt.Errorf("text style %s's line height: %w", s.Name, err)
		}
		tracking, err := length(s.LetterSpacing)
		if err != nil {
			return "", fmt.Errorf("text style %s's letter-spacing: %w", s.Name, err)
		}
		if tracking == "normal" {
			tracking = "0px"
		}
		name := "--text-" + slug(s.Name)
		fmt.Fprintf(&b, "  %s: %d %s/%s var(--font-%s);\n  %s-tracking: %s;\n", name, weight, px(s.Size), line, fontKind(s), name, tracking)
	}
	return b.String(), nil
}

// effectTokens are the effect styles' custom properties: --shadow-<name> a box-shadow, its colours the variables
// they're bound to, so a shadow follows the theme.
func effectTokens(styles []effectStyle) (string, error) {
	var b strings.Builder
	b.WriteString("\n  /* Effect styles */\n")
	for _, s := range styles {
		shadows := make([]string, 0, len(s.Effects))
		for _, e := range s.Effects {
			inset := ""
			switch e.Type {
			case "DROP_SHADOW":
			case "INNER_SHADOW":
				inset = "inset "
			default:
				return "", fmt.Errorf("effect style %s has a %s, which tokens.css doesn't carry", s.Name, e.Type)
			}
			color, err := cssValue(e.Color, "COLOR")
			if err != nil {
				return "", fmt.Errorf("effect style %s's colour: %w", s.Name, err)
			}
			shadows = append(shadows, fmt.Sprintf("%s%s %s %s %s %s", inset, px(e.X), px(e.Y), px(e.Radius), px(e.Spread), color))
		}
		fmt.Fprintf(&b, "  --shadow-%s: %s;\n", slug(s.Name), strings.Join(shadows, ", "))
	}
	return b.String(), nil
}

// length is a line height or letter-spacing as CSS: pixels, a percentage, or "normal" (Figma's auto).
func length(raw json.RawMessage) (string, error) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return px(n), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s is neither a number nor a string: %w", raw, err)
	}
	if s == "auto" {
		return "normal", nil
	}
	if strings.HasSuffix(s, "%") {
		return s, nil
	}
	return "", fmt.Errorf("%q is neither pixels, a percentage nor auto", s)
}

// slug is a style's CSS name: Figma's, lower case, with its slashes and spaces hyphens ("Body/Small Strong" is
// body-small-strong).
func slug(name string) string {
	return strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(name))
}
