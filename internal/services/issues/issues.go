// Package issues is the domain of an organization's issues: the work its members create, assign and move through six
// states, each named by the organization's issue key and a number, such as ACME-42. It knows nothing of HTTP or
// PostgreSQL: adapters bring those through its port, Store, and every permission check goes through sign-in's
// authorizer (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md).
package issues

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
)

// State is where an issue stands. Six are fixed, in Linear's order; Done and Canceled close an issue.
type State string

// The six states.
const (
	StateBacklog    State = "backlog"
	StateTodo       State = "todo"
	StateInProgress State = "in_progress"
	StateInReview   State = "in_review"
	StateDone       State = "done"
	StateCanceled   State = "canceled"
)

// States lists the states in their order.
var States = []State{StateBacklog, StateTodo, StateInProgress, StateInReview, StateDone, StateCanceled}

// OpenStates lists the states of an open issue, which a list shows when it names none.
var OpenStates = []State{StateBacklog, StateTodo, StateInProgress, StateInReview}

// Closed reports whether the state closes an issue: Done or Canceled.
func (s State) Closed() bool {
	return s == StateDone || s == StateCanceled
}

// Priority is how urgent an issue is: none, or urgent (1) to low (4), as Linear numbers them.
type Priority int

// The five priorities.
const (
	PriorityNone Priority = iota
	PriorityUrgent
	PriorityHigh
	PriorityMedium
	PriorityLow
)

// priorityNames are the priorities' names, by number.
var priorityNames = []string{"none", "urgent", "high", "medium", "low"}

// String is the priority's name.
func (p Priority) String() string {
	if p < PriorityNone || p > PriorityLow {
		return strconv.Itoa(int(p))
	}
	return priorityNames[p]
}

// ParsePriority is the priority name names, or a *orgs.ValidationError.
func ParsePriority(name string) (Priority, error) {
	i := slices.Index(priorityNames, name)
	if i < 0 {
		return PriorityNone, &orgs.ValidationError{Field: "priority", Code: "priority.invalid", Message: "none, urgent, high, medium or low"}
	}
	return Priority(i), nil
}

// Principal is who an issue is assigned to, who created it, or who changed it.
type Principal struct {
	ID          uuid.UUID
	DisplayName string
}

// Repository is the repository an issue is about.
type Repository struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// Issue is a piece of an organization's work.
type Issue struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// OrgSlug and Key are its organization's slug and issue key as they are now: Key and Number make its identifier.
	OrgSlug string
	Key     string
	Number  int
	Title   string
	// Description is Markdown; DescriptionHTML is it rendered, which only reading one issue answers.
	Description     string
	DescriptionHTML string
	State           State
	Priority        Priority
	// Assignee is who works on it; nil for nobody, or once they are gone.
	Assignee *Principal
	// Repository is the repository it is about; nil for none, or for a viewer who doesn't read it.
	Repository *Repository
	// CreatedBy is who created it; nil once they are gone.
	CreatedBy *Principal
	CreatedAt time.Time
	UpdatedAt time.Time
	// ClosedAt is when it was last Done or Canceled; nil while it is open.
	ClosedAt *time.Time
	// Version counts its changes from 1: it is its ETag, which a change names to refuse one made meanwhile.
	Version int64
}

// Identifier is how people name the issue: its organization's key and its number, such as ACME-42.
func (i Issue) Identifier() string {
	return fmt.Sprintf("%s-%d", i.Key, i.Number)
}

// Input is what creates an issue. A zero State is Backlog; a nil AssigneeID or RepositoryID is none.
type Input struct {
	Title        string
	Description  string
	State        State
	Priority     Priority
	AssigneeID   uuid.UUID
	RepositoryID uuid.UUID
}

// Patch is what changes an issue: each field that isn't nil. uuid.Nil unassigns it, or makes it about no repository.
type Patch struct {
	Title        *string
	Description  *string
	State        *State
	Priority     *Priority
	AssigneeID   *uuid.UUID
	RepositoryID *uuid.UUID
	// Version is the version of the issue the change was made from, which refuses one made meanwhile with
	// ErrChanged; 0 changes whichever is there.
	Version int64
}

// Filter is which of an organization's issues a list shows.
type Filter struct {
	// States are the states it shows; empty, the open ones.
	States []State
	// AssigneeID shows those assigned to one principal, or to nobody for uuid.Nil; nil, anyone's.
	AssigneeID *uuid.UUID
	// RepositoryID shows those about one repository; nil, any.
	RepositoryID *uuid.UUID
	// Text shows those whose title or description holds it, in any case, or whose identifier it is.
	Text string
	// Cursor is where the page starts: a page's Next; "" for the first.
	Cursor string
	// Limit is the page's size: DefaultPageSize for 0, at most MaxPageSize.
	Limit int
}

// The sizes of a list's pages.
const (
	DefaultPageSize = 50
	MaxPageSize     = 100
)

// Page is a page of a list, newest first, and the cursor of the next; "" after the last.
type Page struct {
	Issues []Issue
	Next   string
}

// Query is a Filter as the store runs it: its states and text resolved, and its cursor decoded into the number the
// page's issues are below; 0 from the newest.
type Query struct {
	States       []State
	AssigneeID   *uuid.UUID
	RepositoryID *uuid.UUID
	Text         string
	Number       int
	Below        int
	Limit        int
}

// Change is a field an issue's change changed, with its value before and after as people read it: a state's and a
// priority's names, an assignee's display name, a repository's slug; "" for none. FromID and ToID are the assignee or
// repository they name.
type Change struct {
	Field  string
	From   string
	To     string
	FromID uuid.UUID
	ToID   uuid.UUID
}

// Event is something that happened to an issue, by whom and when: issue.created, or issue.changed with its changes.
type Event struct {
	ID   uuid.UUID
	Kind string
	// Actor is who did it; nil once they are gone.
	Actor   *Principal
	At      time.Time
	Changes []Change
}

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is an issue that isn't there, an organization the viewer may not see, or an assignee or repository
	// that isn't the organization's.
	ErrNotFound = errors.New("not found")
	// ErrChanged is a change or deletion that names a version of an issue it no longer is.
	ErrChanged = errors.New("the issue changed since it was read")
)

// The longest title and description, in characters.
const (
	MaxTitle       = 200
	MaxDescription = 65536
)

// Stale reports whether a change that names version want is refused by an issue at version got.
func Stale(want, got int64) bool {
	return want > 0 && want != got
}

// ValidateTitle normalizes a title, or refuses it: 1 to 200 characters on one line, trimmed.
func ValidateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > MaxTitle || strings.ContainsAny(title, "\r\n") {
		return title, &orgs.ValidationError{Field: "title", Code: "title.invalid", Message: "a title is 1 to 200 characters on one line"}
	}
	return title, nil
}

// ValidateDescription refuses a description of more than 65,536 characters, or that isn't UTF-8.
func ValidateDescription(description string) error {
	if !utf8.ValidString(description) || utf8.RuneCountInString(description) > MaxDescription {
		return &orgs.ValidationError{Field: "description", Code: "description.invalid", Message: "a description is at most 65536 characters"}
	}
	return nil
}

// ValidateState refuses a state that isn't one of the six.
func ValidateState(field string, s State) error {
	if !slices.Contains(States, s) {
		return &orgs.ValidationError{Field: field, Code: "state.invalid", Message: "backlog, todo, in_progress, in_review, done or canceled"}
	}
	return nil
}

// ValidatePriority refuses a priority that isn't one of the five.
func ValidatePriority(p Priority) error {
	if p < PriorityNone || p > PriorityLow {
		return &orgs.ValidationError{Field: "priority", Code: "priority.invalid", Message: "none, urgent, high, medium or low"}
	}
	return nil
}

// Validate normalizes in, or refuses it: its title, description, state (Backlog when empty) and priority.
func Validate(in Input) (Input, error) {
	if in.State == "" {
		in.State = StateBacklog
	}
	title, err := ValidateTitle(in.Title)
	in.Title = title
	if err := errors.Join(err, ValidateDescription(in.Description), ValidateState("state", in.State), ValidatePriority(in.Priority)); err != nil {
		return in, err
	}
	return in, nil
}

// cursor encodes the number a page's issues are below, opaquely.
func cursor(below int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("n" + strconv.Itoa(below)))
}

// decodeCursor is the number a cursor's page is below, or a *orgs.ValidationError; 0 for "".
func decodeCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil && len(raw) > 1 && raw[0] == 'n' {
		if n, err := strconv.Atoi(string(raw[1:])); err == nil && n > 1 {
			return n, nil
		}
	}
	return 0, &orgs.ValidationError{Field: "cursor", Code: "cursor.invalid", Message: "not a page of this list"}
}

// ParseIdentifier is the number an identifier such as ACME-42 names, whatever its key and case, or of a bare number
// such as 42; false for anything else.
func ParseIdentifier(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		key := s[:i]
		if key == "" || strings.IndexFunc(key, func(r rune) bool { return !isAlnum(r) }) >= 0 {
			return 0, false
		}
		s = s[i+1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || strings.HasPrefix(s, "+") {
		return 0, false
	}
	return n, true
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
