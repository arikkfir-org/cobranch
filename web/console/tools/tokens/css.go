package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// darkModes are the modes, beyond each collection's first, that tokens.css carries, by the theme that turns them on.
var darkModes = map[string]bool{"Dark": true}

// fallbacks are the families each text style's typeface falls back on, while it loads or where it lacks a glyph.
var fallbacks = map[string]string{
	"sans": `system-ui, -apple-system, "Segoe UI", sans-serif`,
	"mono": `ui-monospace, "SF Mono", Menlo, monospace`,
}

// weights are the CSS weights of the font styles Figma names.
var weights = map[string]int{"Thin": 100, "Extra Light": 200, "Light": 300, "Regular": 400, "Medium": 500,
	"Semi Bold": 600, "Bold": 700, "Extra Bold": 800, "Black": 900}

// stylesheet is tokens.css for the export: :root with the fonts, every collection's first mode, the text and effect
// styles, then the dark mode twice, for the system's dark theme unless the person chose light, and for their choice.
func stylesheet(e export) (string, error) {
	fonts, err := fontFamilies(e.TextStyles)
	if err != nil {
		return "", err
	}
	light, dark, err := modes(e.Collections)
	if err != nil {
		return "", err
	}
	texts, err := textTokens(e.TextStyles, fonts)
	if err != nil {
		return "", err
	}
	effects, err := effectTokens(e.EffectStyles)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/* Cobranch's design tokens, written by web/console/tools (go generate) from tokens.json, the export of the\n"+
		"   Figma file %s, which is their source: a change starts in Figma and is exported, never made\n"+
		"   here. Every variable under the CSS name the file gives it; light is the default, and dark follows the system\n"+
		"   unless data-theme on <html> names the person's choice. Text styles are font shorthands, their letter-spacing\n"+
		"   beside them; effect styles are box-shadows. */\n:root {\n  color-scheme: light;\n", e.File)
	for _, kind := range []string{"sans", "mono"} {
		fmt.Fprintf(&b, "  --font-%s: %q, %s;\n", kind, fonts[kind], fallbacks[kind])
	}
	b.WriteString(light + texts + effects + "}\n")
	if dark != "" {
		b.WriteString("\n@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {\n    color-scheme: dark;\n" +
			indent(dark) + "  }\n}\n:root[data-theme=\"dark\"] {\n  color-scheme: dark;\n" + dark + "}\n")
	}
	return b.String(), nil
}

// fontFamilies are the typefaces of the text styles: the family of the Code styles is the monospaced one, and that
// of the others the sans-serif one; each kind must have exactly one.
func fontFamilies(styles []textStyle) (map[string]string, error) {
	fonts := map[string]string{}
	for _, s := range styles {
		kind := fontKind(s)
		if f, ok := fonts[kind]; ok && f != s.Family {
			return nil, fmt.Errorf("text style %s is in %s, but other %s styles are in %s", s.Name, s.Family, kind, f)
		}
		fonts[kind] = s.Family
	}
	for _, kind := range []string{"sans", "mono"} {
		if fonts[kind] == "" {
			return nil, fmt.Errorf("no text style names the %s typeface", kind)
		}
	}
	return fonts, nil
}

func fontKind(s textStyle) string {
	if strings.HasPrefix(s.Name, "Code/") {
		return "mono"
	}
	return "sans"
}

// modes are the custom properties of every collection's first mode, and of its dark mode where it has one.
func modes(collections []collection) (string, string, error) {
	var light, dark strings.Builder
	for _, c := range collections {
		if len(c.Modes) == 0 {
			return "", "", fmt.Errorf("collection %s has no modes", c.Name)
		}
		if len(c.Modes) > 1 {
			fmt.Fprintf(&light, "\n  /* %s, %s */\n", c.Name, c.Modes[0])
		} else {
			fmt.Fprintf(&light, "\n  /* %s */\n", c.Name)
		}
		for _, mode := range c.Modes[1:] {
			if !darkModes[mode] {
				return "", "", fmt.Errorf("collection %s has mode %s, which tokens.css doesn't carry", c.Name, mode)
			}
		}
		for _, v := range c.Variables {
			if err := declare(&light, v, c.Modes[0], true); err != nil {
				return "", "", err
			}
			for _, mode := range c.Modes[1:] {
				if err := declare(&dark, v, mode, false); err != nil {
					return "", "", err
				}
			}
		}
	}
	return light.String(), dark.String(), nil
}

// declare writes the custom property of a variable in a mode, and its description when described.
func declare(b *strings.Builder, v variable, mode string, described bool) error {
	if !strings.HasPrefix(v.CSS, "--") {
		return fmt.Errorf("variable %s has CSS name %q in codeSyntax, which isn't a custom property", v.Name, v.CSS)
	}
	value, err := cssValue(v.Values[mode], v.Type)
	if err != nil {
		return fmt.Errorf("variable %s in mode %s: %w", v.Name, mode, err)
	}
	fmt.Fprintf(b, "  %s: %s;", v.CSS, value)
	if described && v.Description != "" {
		fmt.Fprintf(b, " /* %s */", strings.ReplaceAll(v.Description, "*/", "* /"))
	}
	b.WriteString("\n")
	return nil
}

// cssValue is a variable's value as CSS: another variable by its name, a colour, or a number of pixels.
func cssValue(raw json.RawMessage, kind string) (string, error) {
	var alias struct {
		Alias string `json:"alias"`
	}
	if json.Unmarshal(raw, &alias) == nil && alias.Alias != "" {
		return "var(" + alias.Alias + ")", nil
	}
	switch kind {
	case "COLOR":
		var c string
		if err := json.Unmarshal(raw, &c); err != nil || !strings.HasPrefix(c, "#") {
			return "", fmt.Errorf("colour %s isn't a hex colour", raw)
		}
		return c, nil
	case "FLOAT":
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			return "", fmt.Errorf("number %s isn't a number: %w", raw, err)
		}
		return px(n), nil
	}
	return "", fmt.Errorf("type %s has no CSS value", kind)
}

// px is a length in pixels, to the hundredth.
func px(n float64) string {
	return strconv.FormatFloat(float64(int64(n*100+sign(n)*0.5))/100, 'f', -1, 64) + "px"
}

func sign(n float64) float64 {
	if n < 0 {
		return -1
	}
	return 1
}

// indent indents every line of s by two more spaces.
func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n  ") + "\n"
}
