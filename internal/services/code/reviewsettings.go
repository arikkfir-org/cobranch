package code

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"cobranch.dev/internal/services/git"
)

const (
	// MaxReviewInstructions is the longest instructions a git.ReviewSettingsFile adds, in characters.
	MaxReviewInstructions = 20000
	// MaxReviewIncludes is the most paths or patterns a git.ReviewSettingsFile includes.
	MaxReviewIncludes = 20
	// MaxReviewIgnores is the most patterns a git.ReviewSettingsFile ignores.
	MaxReviewIgnores = 100
)

// ReviewSettingsKeys are the top-level keys a git.ReviewSettingsFile may have.
var ReviewSettingsKeys = []string{"enabled", "branches", "paths", "instructions", "include", "maxDiffLines"}

// ParseReviewSettings reads a git.ReviewSettingsFile, or answers a *SettingsError naming the line at fault: every key is
// checked, so a misspelt one refuses the file rather than being ignored. An empty file is the defaults.
func ParseReviewSettings(content []byte) (git.ReviewSettings, error) {
	out, err := parseReviewSettings(content)
	if bad := (*SettingsError)(nil); errors.As(err, &bad) {
		bad.File = git.ReviewSettingsFile
	}
	return out, err
}

// parseReviewSettings is ParseReviewSettings, its errors naming no file.
func parseReviewSettings(content []byte) (git.ReviewSettings, error) {
	out := git.DefaultReviewSettings()
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&doc); errors.Is(err, io.EOF) {
		return out, nil
	} else if err != nil {
		return git.ReviewSettings{}, yamlError(err)
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return out, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return git.ReviewSettings{}, &SettingsError{Line: root.Line, Message: "the settings must be a mapping"}
	}
	if err := onlyKeys(root, "the settings", ReviewSettingsKeys...); err != nil {
		return git.ReviewSettings{}, err
	}
	if err := reviewEnabled(&out, valueOf(root, "enabled")); err != nil {
		return git.ReviewSettings{}, err
	}
	if err := reviewPaths(&out, root); err != nil {
		return git.ReviewSettings{}, err
	}
	if err := reviewInstructions(&out, valueOf(root, "instructions")); err != nil {
		return git.ReviewSettings{}, err
	}
	if err := reviewMaxDiffLines(&out, valueOf(root, "maxDiffLines")); err != nil {
		return git.ReviewSettings{}, err
	}
	return out, nil
}

// reviewEnabled reads enabled, true when left out.
func reviewEnabled(out *git.ReviewSettings, node *yaml.Node) error {
	if node == nil || isNull(node) {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&out.Enabled) != nil {
		return &SettingsError{Line: node.Line, Message: "enabled must be true or false"}
	}
	return nil
}

// reviewPaths reads the lists of patterns and paths: branches, paths' ignore, and include.
func reviewPaths(out *git.ReviewSettings, root *yaml.Node) error {
	if n := valueOf(root, "branches"); n != nil && !isNull(n) {
		branches, err := patterns(n, "branches")
		if err != nil {
			return err
		}
		out.Branches = branches
	}
	if n := valueOf(root, "paths"); n != nil && !isNull(n) {
		if err := onlyKeys(n, "paths", "ignore"); err != nil {
			return err
		}
		ignore, err := pathList(valueOf(n, "ignore"), "ignore", MaxReviewIgnores)
		if err != nil {
			return err
		}
		out.Ignore = ignore
	}
	include, err := pathList(valueOf(root, "include"), "include", MaxReviewIncludes)
	if err != nil {
		return err
	}
	out.Include = include
	return nil
}

// pathList reads a list of at most most paths or patterns of paths under key, each once: relative to the root, at most
// maxAnchorPath bytes; empty when the key is absent.
func pathList(node *yaml.Node, key string, most int) ([]string, error) {
	if node == nil || isNull(node) {
		return []string{}, nil
	}
	list, err := distinctList(node, key, key+" must be a list of paths from the root, or their patterns such as docs/**/*.md", validPathPattern)
	if err != nil {
		return nil, err
	}
	if len(list) > most {
		return nil, &SettingsError{Line: node.Line, Message: fmt.Sprintf("%s names at most %d paths or patterns", key, most)}
	}
	return list, nil
}

// validPathPattern reports whether p names files by their path from the root: UTF-8, at most maxAnchorPath bytes, not
// starting with /.
func validPathPattern(p string) bool {
	return utf8.ValidString(p) && len(p) <= maxAnchorPath && !strings.HasPrefix(p, "/")
}

// reviewInstructions reads instructions: text of at most MaxReviewInstructions characters.
func reviewInstructions(out *git.ReviewSettings, node *yaml.Node) error {
	if node == nil || isNull(node) {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return &SettingsError{Line: node.Line, Message: "instructions must be text"}
	}
	if utf8.RuneCountInString(node.Value) > MaxReviewInstructions {
		return &SettingsError{Line: node.Line, Message: fmt.Sprintf("instructions must be at most %d characters", MaxReviewInstructions)}
	}
	out.Instructions = node.Value
	return nil
}

// reviewMaxDiffLines reads maxDiffLines: a whole number from 1 to MaxDiffLines, the most a diff holds.
func reviewMaxDiffLines(out *git.ReviewSettings, node *yaml.Node) error {
	if node == nil || isNull(node) {
		return nil
	}
	var lines int
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Decode(&lines) != nil || lines < 1 || lines > MaxDiffLines {
		return &SettingsError{Line: node.Line, Message: fmt.Sprintf("maxDiffLines must be a whole number from 1 to %d", MaxDiffLines)}
	}
	out.MaxDiffLines = lines
	return nil
}
