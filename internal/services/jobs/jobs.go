// Package jobs is the domain of a repository's jobs: what /cobranch.yaml declares under jobs, the events that trigger
// them, and the runs they queue. It holds the types every part of Cobranch reads jobs and runs through, and the pure
// rules that match a job to an event; parsing the file is package code's, keeping runs is a store's
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md).
package jobs

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Kind is the kind of work a job does: one of a pod, a script or a webhook.
type Kind string

// The kinds of work.
const (
	KindPod     Kind = "pod"
	KindScript  Kind = "script"
	KindWebhook Kind = "webhook"
)

// MaxTimeout is the longest a run of any job may take, whatever the installation allows.
const MaxTimeout = 24 * time.Hour

// DefaultPermissions are what a run's token may do when its job lists no permissions: read the repository and set
// annotations, sorted as a job's permissions are.
var DefaultPermissions = []string{"annotations.write", "repo.read"}

// Job is one job as a /cobranch.yaml declares it under jobs, by its name: its triggers, its work, the secrets it
// receives, what its runs' tokens may do and how long a run may take.
type Job struct {
	// Name is the job's key under jobs: its principal's name in its repository and the annotation its runs report.
	Name string `json:"name"`
	// On is what triggers it.
	On Triggers `json:"on"`
	// Pod is a Kubernetes pod spec, as JSON; nil unless the job runs a pod.
	Pod json.RawMessage `json:"pod,omitempty"`
	// Script is a Bash script, run in Image; empty unless the job runs a script.
	Script string `json:"script,omitempty"`
	// Image is the container image a script runs in; empty for the installation's script image.
	Image string `json:"image,omitempty"`
	// Webhook is where the job posts its runs; nil unless the job is a webhook.
	Webhook *Webhook `json:"webhook,omitempty"`
	// Secrets maps environment variables to the IDs of the secrets whose values they carry.
	Secrets map[string]uuid.UUID `json:"secrets,omitempty"`
	// Permissions are what a run's token may do, within the repository's job ceiling, sorted.
	Permissions []string `json:"permissions"`
	// Timeout is how long a run may take; zero for the installation's default.
	Timeout Duration `json:"timeout,omitempty"`
	// Checkout is whether a run's pod fetches its commit into /workspace first.
	Checkout bool `json:"checkout"`
}

// Kind is the kind of work the job does.
func (j Job) Kind() Kind {
	switch {
	case j.Webhook != nil:
		return KindWebhook
	case j.Pod != nil:
		return KindPod
	default:
		return KindScript
	}
}

// Webhook is where a webhook job posts its runs, signed with the value of a secret.
type Webhook struct {
	URL           string    `json:"url"`
	SigningSecret uuid.UUID `json:"signingSecret"`
}

// Triggers are the events that trigger a job; a nil field never does.
type Triggers struct {
	Push       *PushTrigger       `json:"push,omitempty"`
	Tag        *TagTrigger        `json:"tag,omitempty"`
	Annotation *AnnotationTrigger `json:"annotation,omitempty"`
	Review     *ReviewTrigger     `json:"review,omitempty"`
	Event      *EventTrigger      `json:"event,omitempty"`
	Schedule   []Schedule         `json:"schedule,omitempty"`
	Manual     *ManualTrigger     `json:"manual,omitempty"`
}

// PushTrigger fires when a push creates or moves a branch that Branches matches, every branch without patterns.
type PushTrigger struct {
	Branches []string `json:"branches,omitempty"`
}

// TagTrigger fires when a push creates or moves a tag that Tags matches, every tag without patterns.
type TagTrigger struct {
	Tags []string `json:"tags,omitempty"`
}

// AnnotationTrigger fires when an annotation that Names lists reaches a status Statuses lists, on the head of a live
// branch that Branches matches.
type AnnotationTrigger struct {
	Names    []string `json:"names"`
	Statuses []string `json:"statuses"`
	Branches []string `json:"branches,omitempty"`
}

// ReviewTrigger fires when a review of a state States lists is submitted on the head of a branch Branches matches.
type ReviewTrigger struct {
	States   []string `json:"states"`
	Branches []string `json:"branches,omitempty"`
}

// EventTrigger fires when a principal posts an event that Names lists.
type EventTrigger struct {
	Names []string `json:"names"`
}

// Schedule fires at each slot of a five-field cron expression, in UTC.
type Schedule struct {
	Cron string `json:"cron"`
}

// ManualTrigger lets a principal with jobs.run start the job on a branch.
type ManualTrigger struct{}

// Duration is a time.Duration that JSON keeps as Go writes durations, such as "30m0s".
type Duration time.Duration

// MarshalJSON writes d as Go writes durations.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String()) //nolint:wrapcheck // a string always marshals
}

// UnmarshalJSON reads d as Go writes durations.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err //nolint:wrapcheck // json's own error, returned to json
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err //nolint:wrapcheck // time's own error, returned to json
	}
	*d = Duration(parsed)
	return nil
}

// OutsidePushes is what happens to the work of a tip pushed from outside the repository's organization: its runs, and
// whatever else follows a push.
type OutsidePushes string

// What outside pushes do.
const (
	// OutsideWait leaves their runs Waiting until a member starts each.
	OutsideWait OutsidePushes = "wait"
	// OutsideRun queues them like any other.
	OutsideRun OutsidePushes = "run"
)

// DefaultCeiling is the repository role every job stays within when /cobranch.yaml names none.
const DefaultCeiling = "Automation"

// RepositorySettings are what the default branch's /cobranch.yaml says under repository: outsidePushes, and jobs'
// ceiling.
type RepositorySettings struct {
	// OutsidePushes is what happens to the work of tips pushed from outside the organization.
	OutsidePushes OutsidePushes `json:"outsidePushes"`
	// Ceiling names the repository role whose permissions bound every job's, repository.jobs.ceiling.
	Ceiling string `json:"ceiling"`
}

// DefaultRepositorySettings are a repository's when its default branch's /cobranch.yaml says nothing of them.
var DefaultRepositorySettings = RepositorySettings{Ceiling: DefaultCeiling, OutsidePushes: OutsideWait}

// EventKind is what happened that may trigger jobs.
type EventKind string

// The kinds of events.
const (
	EventPush       EventKind = "push"
	EventTag        EventKind = "tag"
	EventAnnotation EventKind = "annotation"
	EventReview     EventKind = "review"
	EventPosted     EventKind = "event"
	EventSchedule   EventKind = "schedule"
	EventManual     EventKind = "manual"
)

// Event is something that happened in a repository, which triggers the jobs whose triggers match it.
type Event struct {
	Kind   EventKind `json:"kind"`
	RepoID uuid.UUID `json:"-"`
	// SHA is the commit runs execute at.
	SHA string `json:"sha"`
	// Ref is the branch or tag the event happened on, in full: refs/heads/… or refs/tags/….
	Ref string `json:"ref"`
	// Name is the annotation's name, or the posted event's.
	Name string `json:"name,omitempty"`
	// State is the annotation's status, or the review's state.
	State string `json:"state,omitempty"`
	// ActorID is who caused it: the pusher, the annotation's or review's principal, the poster or the starter.
	ActorID uuid.UUID `json:"actor,omitzero"`
	// PushID is the push that caused it, for a push or a tag.
	PushID uuid.UUID `json:"push,omitzero"`
	// CauseRunID is the run that set the annotation, for an annotation another run set.
	CauseRunID uuid.UUID `json:"causeRun,omitzero"`
}

// State is where a run stands.
type State string

// The states of a run.
const (
	// StateWaiting waits for a member to start it: its tip was pushed from outside the organization.
	StateWaiting State = "waiting"
	// StateQueued waits for the controller.
	StateQueued State = "queued"
	// StateRunning runs.
	StateRunning State = "running"
	// StateSucceeded ended well.
	StateSucceeded State = "succeeded"
	// StateFailed ended badly, or never started.
	StateFailed State = "failed"
	// StateCancelled was cancelled, or superseded by a newer run.
	StateCancelled State = "cancelled"
)

// Active reports whether a run in state s has yet to end.
func (s State) Active() bool {
	return s == StateWaiting || s == StateQueued || s == StateRunning
}
