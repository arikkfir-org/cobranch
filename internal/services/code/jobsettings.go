package code

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
)

// repositorySettings reads the repository key of a SettingsFile: what happens to the work of tips pushed from outside
// the organization, and the ceiling of its jobs.
func repositorySettings(node *yaml.Node) (jobs.RepositorySettings, error) {
	out := jobs.DefaultRepositorySettings
	if node == nil || isNull(node) {
		return out, nil
	}
	if err := onlyKeys(node, "repository", "outsidePushes", "jobs"); err != nil {
		return out, err
	}
	if n := valueOf(node, "outsidePushes"); n != nil && !isNull(n) {
		v, err := stringOf(n, "outsidePushes must be wait or run")
		if err != nil {
			return out, err
		}
		if v != string(jobs.OutsideWait) && v != string(jobs.OutsideRun) {
			return out, &SettingsError{Line: n.Line, Message: "outsidePushes must be wait or run"}
		}
		out.OutsidePushes = jobs.OutsidePushes(v)
	}
	settings := valueOf(node, "jobs")
	if settings == nil || isNull(settings) {
		return out, nil
	}
	if err := onlyKeys(settings, "repository.jobs", "ceiling"); err != nil {
		return out, err
	}
	if n := valueOf(settings, "ceiling"); n != nil && !isNull(n) {
		ceiling, err := stringOf(n, "ceiling must name a repository role")
		if err != nil {
			return out, err
		}
		out.Ceiling = ceiling
	}
	return out, nil
}

// reservedJobPrefix starts the annotations that deliveries report, which no job's name may.
const reservedJobPrefix = "delivery/"

// parseJobs reads the jobs key of a SettingsFile: one job per name, by name.
func parseJobs(node *yaml.Node) ([]jobs.Job, error) {
	out := []jobs.Job{}
	if node == nil || isNull(node) {
		return out, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: node.Line, Message: "jobs must map job names to their jobs"}
	}
	if err := uniqueKeys(node, "jobs"); err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || ValidateAnnotationName(key.Value) != nil {
			return nil, &SettingsError{Line: key.Line, Message: "a job's name must be 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"}
		}
		if strings.HasPrefix(key.Value, reservedJobPrefix) {
			return nil, &SettingsError{Line: key.Line, Message: "a job's name can't start with " + reservedJobPrefix + ", which deliveries report"}
		}
		job, err := parseJob(key.Value, value)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	slices.SortFunc(out, func(a, b jobs.Job) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// parseJob reads one job under jobs, of name.
func parseJob(name string, node *yaml.Node) (jobs.Job, error) {
	job := jobs.Job{Name: name, Permissions: slices.Clone(jobs.DefaultPermissions), Checkout: true}
	what := "job " + name
	if isNull(node) {
		return job, &SettingsError{Line: node.Line, Message: what + " needs on and one of pod, script or webhook"}
	}
	if err := onlyKeys(node, what, "on", "pod", "script", "image", "webhook", "secrets", "permissions", "timeout", "checkout"); err != nil {
		return job, err
	}
	var err error
	if job.On, err = parseTriggers(name, valueOf(node, "on"), node.Line); err != nil {
		return job, err
	}
	if err := parseWork(&job, node); err != nil {
		return job, err
	}
	if job.Secrets, err = parseSecrets(valueOf(node, "secrets")); err != nil {
		return job, err
	}
	if n := valueOf(node, "permissions"); n != nil && !isNull(n) {
		if job.Permissions, err = parsePermissions(n); err != nil {
			return job, err
		}
	}
	if n := valueOf(node, "timeout"); n != nil && !isNull(n) {
		if job.Timeout, err = parseTimeout(n); err != nil {
			return job, err
		}
	}
	if n := valueOf(node, "checkout"); n != nil && !isNull(n) {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&job.Checkout) != nil {
			return job, &SettingsError{Line: n.Line, Message: "checkout must be true or false"}
		}
	}
	return job, nil
}

// parseWork reads the work a job does: exactly one of pod, script, with an optional image, and webhook.
func parseWork(job *jobs.Job, node *yaml.Node) error {
	pod, script, webhook, image := valueOf(node, "pod"), valueOf(node, "script"), valueOf(node, "webhook"), valueOf(node, "image")
	count := 0
	for _, n := range []*yaml.Node{pod, script, webhook} {
		if n != nil {
			count++
		}
	}
	if count != 1 {
		return &SettingsError{Line: node.Line, Message: "job " + job.Name + " needs exactly one of pod, script or webhook"}
	}
	if image != nil && script == nil {
		return &SettingsError{Line: image.Line, Message: "image goes with script only"}
	}
	var err error
	switch {
	case pod != nil:
		job.Pod, err = parsePod(pod)
	case webhook != nil:
		job.Webhook, err = parseWebhook(webhook)
	default:
		if job.Script, err = stringOf(script, "script must be a Bash script"); err == nil && image != nil {
			job.Image, err = stringOf(image, "image must name a container image")
		}
	}
	return err
}

// parsePod reads a job's pod spec, checked against what jobs may use, as JSON.
func parsePod(node *yaml.Node) (json.RawMessage, error) {
	var spec map[string]any
	if node.Kind != yaml.MappingNode || node.Decode(&spec) != nil {
		return nil, &SettingsError{Line: node.Line, Message: "pod must be a Kubernetes pod spec"}
	}
	if err := jobs.CheckPod(spec); err != nil {
		return nil, &SettingsError{Line: node.Line, Message: err.Error()}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, &SettingsError{Line: node.Line, Message: "pod must be a Kubernetes pod spec: " + err.Error()}
	}
	return encoded, nil
}

// parseWebhook reads where a webhook job posts its runs: an https URL, and the ID of the secret that signs them.
func parseWebhook(node *yaml.Node) (*jobs.Webhook, error) {
	if err := onlyKeys(node, "webhook", "url", "signingSecret"); err != nil {
		return nil, err
	}
	n := valueOf(node, "url")
	if n == nil {
		return nil, &SettingsError{Line: node.Line, Message: "webhook needs a url"}
	}
	raw, err := stringOf(n, "url must be an https URL")
	if err != nil {
		return nil, err
	}
	if u, err := url.Parse(raw); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, &SettingsError{Line: n.Line, Message: "url must be an https URL"}
	}
	secret := valueOf(node, "signingSecret")
	if secret == nil {
		return nil, &SettingsError{Line: node.Line, Message: "webhook needs a signingSecret, a secret's ID"}
	}
	id, err := uuidOf(secret, "signingSecret must be a secret's ID")
	if err != nil {
		return nil, err
	}
	return &jobs.Webhook{URL: raw, SigningSecret: id}, nil
}

// envName is an environment variable's name a job's secret may take.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// parseSecrets reads the secrets a job receives: environment variables' names, none of Cobranch's own COBRANCH_, each
// mapped to a secret's ID.
func parseSecrets(node *yaml.Node) (map[string]uuid.UUID, error) {
	if node == nil || isNull(node) {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, &SettingsError{Line: node.Line, Message: "secrets must map environment variables to secrets' IDs"}
	}
	if err := uniqueKeys(node, "secrets"); err != nil {
		return nil, err
	}
	out := map[string]uuid.UUID{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if !envName.MatchString(key.Value) || strings.HasPrefix(strings.ToUpper(key.Value), "COBRANCH_") {
			return nil, &SettingsError{Line: key.Line, Message: fmt.Sprintf("%q is no environment variable a secret may take", key.Value)}
		}
		id, err := uuidOf(node.Content[i+1], "a secret is named by its ID")
		if err != nil {
			return nil, err
		}
		out[key.Value] = id
	}
	return out, nil
}

// parsePermissions reads what a job's runs may do: repository permissions, each once, sorted.
func parsePermissions(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, &SettingsError{Line: node.Line, Message: "permissions must be a list of repository permissions"}
	}
	out := []string{}
	for _, n := range node.Content {
		if n.Kind != yaml.ScalarNode || !slices.Contains(auth.RepositoryPermissions, n.Value) {
			return nil, &SettingsError{Line: n.Line, Message: fmt.Sprintf("%q is no repository permission", n.Value)}
		}
		if slices.Contains(out, n.Value) {
			return nil, &SettingsError{Line: n.Line, Message: "permission " + n.Value + " is listed twice"}
		}
		out = append(out, n.Value)
	}
	slices.Sort(out)
	return out, nil
}

// parseTimeout reads how long a job's run may take: a duration such as 30m or 1h30m, at most jobs.MaxTimeout.
func parseTimeout(node *yaml.Node) (jobs.Duration, error) {
	const message = "timeout must be a duration such as 30m or 2h, at most 24h"
	s, err := stringOf(node, message)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > jobs.MaxTimeout {
		return 0, &SettingsError{Line: node.Line, Message: message}
	}
	return jobs.Duration(d), nil
}

// stringOf reads a non-empty string, or answers a *SettingsError of message.
func stringOf(node *yaml.Node, message string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
		return "", &SettingsError{Line: node.Line, Message: message}
	}
	return node.Value, nil
}

// uuidOf reads a UUID, or answers a *SettingsError of message.
func uuidOf(node *yaml.Node, message string) (uuid.UUID, error) {
	s, err := stringOf(node, message)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil || len(s) != 36 {
		return uuid.Nil, &SettingsError{Line: node.Line, Message: message}
	}
	return id, nil
}
