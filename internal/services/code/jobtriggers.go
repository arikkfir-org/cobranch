package code

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"cobranch.dev/internal/services/jobs"
)

// reviewStates are the states a review may be submitted in.
var reviewStates = []string{"approved", "changes_requested", "neutral"}

// parseTriggers reads the on key of job name, which line holds: one or more of its triggers.
func parseTriggers(name string, node *yaml.Node, line int) (jobs.Triggers, error) {
	var on jobs.Triggers
	if node == nil || isNull(node) {
		return on, &SettingsError{Line: line, Message: "job " + name + " needs on, naming what triggers it"}
	}
	if err := onlyKeys(node, "on", "push", "tag", "annotation", "review", "event", "schedule", "manual"); err != nil {
		return on, err
	}
	if len(node.Content) == 0 {
		return on, &SettingsError{Line: node.Line, Message: "job " + name + " needs a trigger under on"}
	}
	var err error
	for i := 0; i+1 < len(node.Content) && err == nil; i += 2 {
		value := node.Content[i+1]
		switch node.Content[i].Value {
		case "push":
			on.Push = &jobs.PushTrigger{}
			on.Push.Branches, err = patternsUnder(value, "push", "branches")
		case "tag":
			on.Tag = &jobs.TagTrigger{}
			on.Tag.Tags, err = patternsUnder(value, "tag", "tags")
		case "annotation":
			on.Annotation, err = annotationTrigger(name, value)
		case "review":
			on.Review, err = reviewTrigger(value)
		case "event":
			on.Event, err = eventTrigger(value)
		case "schedule":
			on.Schedule, err = schedules(value)
		case "manual":
			on.Manual, err = &jobs.ManualTrigger{}, emptyTrigger(value, "manual")
		}
	}
	return on, err
}

// patternsUnder reads a trigger that takes only a list of branches' or tags' patterns, under key; none, or a null
// trigger, match every one.
func patternsUnder(node *yaml.Node, trigger, key string) ([]string, error) {
	if isNull(node) {
		return nil, nil
	}
	if err := onlyKeys(node, trigger, key); err != nil {
		return nil, err
	}
	return patterns(valueOf(node, key), key)
}

// patterns reads a list of branches' or tags' patterns, each once; nil when the key is absent.
func patterns(node *yaml.Node, key string) ([]string, error) {
	if node == nil || isNull(node) {
		return nil, nil
	}
	return stringList(node, key, key+" must be a list of names or patterns such as release/*", func(string) bool { return true })
}

// stringList reads a non-empty list of distinct non-empty strings that valid accepts, or answers message.
func stringList(node *yaml.Node, what, message string, valid func(string) bool) ([]string, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, &SettingsError{Line: node.Line, Message: message}
	}
	out := make([]string, 0, len(node.Content))
	for _, n := range node.Content {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value == "" || !valid(n.Value) {
			return nil, &SettingsError{Line: n.Line, Message: message}
		}
		if slices.Contains(out, n.Value) {
			return nil, &SettingsError{Line: n.Line, Message: fmt.Sprintf("%s names %q twice", what, n.Value)}
		}
		out = append(out, n.Value)
	}
	return out, nil
}

// annotationTrigger reads trigger annotation of job name: the annotations it follows, never the job's own, the statuses
// they reach, Successful by default, and the branches whose heads they are on.
func annotationTrigger(name string, node *yaml.Node) (*jobs.AnnotationTrigger, error) {
	if err := onlyKeys(node, "annotation", "names", "statuses", "branches"); err != nil {
		return nil, err
	}
	names := valueOf(node, "names")
	if names == nil {
		return nil, &SettingsError{Line: node.Line, Message: "annotation needs names, the annotations it follows"}
	}
	t := &jobs.AnnotationTrigger{Statuses: []string{string(AnnotationSuccessful)}}
	var err error
	if t.Names, err = annotationNames(names); err != nil {
		return nil, err
	}
	if slices.Contains(t.Names, name) {
		return nil, &SettingsError{Line: names.Line, Message: "job " + name + " can't follow its own annotation"}
	}
	if n := valueOf(node, "statuses"); n != nil {
		valid := func(s string) bool { return slices.Contains(AnnotationStatuses, AnnotationStatus(s)) }
		if t.Statuses, err = stringList(n, "statuses", "statuses must list pending, in_progress, failed or successful", valid); err != nil {
			return nil, err
		}
	}
	t.Branches, err = patterns(valueOf(node, "branches"), "branches")
	return t, err
}

// reviewTrigger reads trigger review: the states of the reviews it follows, approved by default, and the branches.
func reviewTrigger(node *yaml.Node) (*jobs.ReviewTrigger, error) {
	t := &jobs.ReviewTrigger{States: []string{"approved"}}
	if isNull(node) {
		return t, nil
	}
	if err := onlyKeys(node, "review", "states", "branches"); err != nil {
		return nil, err
	}
	var err error
	if n := valueOf(node, "states"); n != nil {
		valid := func(s string) bool { return slices.Contains(reviewStates, s) }
		if t.States, err = stringList(n, "states", "states must list approved, changes_requested or neutral", valid); err != nil {
			return nil, err
		}
	}
	t.Branches, err = patterns(valueOf(node, "branches"), "branches")
	return t, err
}

// eventTrigger reads trigger event: the names of the events it follows, each as an annotation's name is written.
func eventTrigger(node *yaml.Node) (*jobs.EventTrigger, error) {
	if err := onlyKeys(node, "event", "names"); err != nil {
		return nil, err
	}
	names := valueOf(node, "names")
	if names == nil {
		return nil, &SettingsError{Line: node.Line, Message: "event needs names, the events it follows"}
	}
	valid := func(s string) bool { return ValidateAnnotationName(s) == nil }
	list, err := stringList(names, "names", "names must list events' names: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/'", valid)
	if err != nil {
		return nil, err
	}
	return &jobs.EventTrigger{Names: list}, nil
}

// schedules reads trigger schedule: a list of five-field cron expressions, in UTC.
func schedules(node *yaml.Node) ([]jobs.Schedule, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, &SettingsError{Line: node.Line, Message: "schedule must be a list of {cron: ...}"}
	}
	out := make([]jobs.Schedule, 0, len(node.Content))
	for _, n := range node.Content {
		if err := onlyKeys(n, "a schedule", "cron"); err != nil {
			return nil, err
		}
		c := valueOf(n, "cron")
		if c == nil {
			return nil, &SettingsError{Line: n.Line, Message: "a schedule needs a cron expression"}
		}
		expr, err := stringOf(c, "cron must be a five-field cron expression")
		if err != nil {
			return nil, err
		}
		if _, err := jobs.ParseCron(expr); err != nil {
			return nil, &SettingsError{Line: c.Line, Message: err.Error()}
		}
		out = append(out, jobs.Schedule{Cron: expr})
	}
	return out, nil
}

// emptyTrigger checks a trigger that takes nothing: null or an empty mapping.
func emptyTrigger(node *yaml.Node, trigger string) error {
	if isNull(node) {
		return nil
	}
	return onlyKeys(node, trigger)
}
