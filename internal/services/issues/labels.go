package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
)

// Color is a label's colour: one of five, which the Console draws in the design system's tones, so that a label reads
// in both themes.
type Color string

// The five colours.
const (
	ColorGray   Color = "gray"
	ColorBlue   Color = "blue"
	ColorGreen  Color = "green"
	ColorYellow Color = "yellow"
	ColorRed    Color = "red"
)

// Colors lists the colours.
var Colors = []Color{ColorGray, ColorBlue, ColorGreen, ColorYellow, ColorRed}

// Label is one of an organization's labels, which its issues carry.
type Label struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Color       Color
	Description string
	// Issues is how many issues carry it.
	Issues    int
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts its changes from 1: it is its ETag.
	Version int64
}

// IssueLabel is a label as an issue shows it.
type IssueLabel struct {
	ID    uuid.UUID
	Name  string
	Color Color
}

// LabelInput is what creates a label. A zero Color is gray.
type LabelInput struct {
	Name        string
	Color       Color
	Description string
}

// LabelPatch is what changes a label: each field that isn't nil.
type LabelPatch struct {
	Name        *string
	Color       *Color
	Description *string
	// Version is the version of the label the change was made from, which refuses one made meanwhile with
	// ErrLabelChanged; 0 changes whichever is there.
	Version int64
}

// LabelStore is what the database keeps of an organization's labels. Each write is one transaction that acts for
// actor.
type LabelStore interface {
	// Labels lists the organization's labels by name.
	Labels(ctx context.Context, orgID uuid.UUID) ([]Label, error)
	// Label finds one of the organization's labels, or ErrLabelNotFound.
	Label(ctx context.Context, orgID, labelID uuid.UUID) (Label, error)
	// LabelsOf finds those of ids that are the organization's labels, by name.
	LabelsOf(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]IssueLabel, error)
	// CreateLabel records the label and answers it as kept, or ErrLabelTaken when the organization has one of its name.
	CreateLabel(ctx context.Context, actor uuid.UUID, label Label) (Label, error)
	// UpdateLabel writes label's fields over the one at version, and counts a change on the issues that carry it when
	// its name or colour changes, or ErrLabelChanged, ErrLabelNotFound or ErrLabelTaken.
	UpdateLabel(ctx context.Context, actor uuid.UUID, label Label, version int64) (Label, error)
	// DeleteLabel deletes the label at version, which takes it off its issues and counts a change on them, or
	// ErrLabelChanged or ErrLabelNotFound.
	DeleteLabel(ctx context.Context, actor uuid.UUID, label Label, version int64) error
}

// The errors of labels.
var (
	// ErrLabelNotFound is a label that isn't the organization's.
	ErrLabelNotFound = errors.New("label not found")
	// ErrLabelChanged is a change or deletion that names a version of a label it no longer is.
	ErrLabelChanged = errors.New("the label changed since it was read")
	// ErrLabelTaken is a label named as another of the organization's is, whatever their case.
	ErrLabelTaken = errors.New("the organization has a label of that name")
)

// The longest label name and description, in characters, and the most labels an issue carries.
const (
	MaxLabelName        = 50
	MaxLabelDescription = 200
	MaxIssueLabels      = 20
)

// ValidateLabelName normalizes a label's name, or refuses it: 1 to 50 characters on one line, trimmed.
func ValidateLabelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxLabelName || strings.ContainsAny(name, "\r\n") {
		return name, &orgs.ValidationError{Field: "name", Code: "name.invalid", Message: "a label's name is 1 to 50 characters on one line"}
	}
	return name, nil
}

// ValidateColor refuses a colour that isn't one of the five.
func ValidateColor(c Color) error {
	if !slices.Contains(Colors, c) {
		return &orgs.ValidationError{Field: "color", Code: "color.invalid", Message: "gray, blue, green, yellow or red"}
	}
	return nil
}

// ValidateLabelDescription refuses a description of more than 200 characters, or on more than one line.
func ValidateLabelDescription(d string) error {
	if !utf8.ValidString(d) || utf8.RuneCountInString(d) > MaxLabelDescription || strings.ContainsAny(d, "\r\n") {
		return &orgs.ValidationError{Field: "description", Code: "description.invalid", Message: "a label's description is at most 200 characters on one line"}
	}
	return nil
}

// Labels lists the organization's labels by name; it takes issues.read.
func (s *Service) Labels(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Label, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Labels")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	if err := s.require(ctx, v, orgID, PermRead); err != nil {
		return nil, failed(span, "listing the labels", err)
	}
	labels, err := s.labels.Labels(ctx, orgID)
	if err != nil {
		return nil, failed(span, "listing the labels", err)
	}
	return labels, nil
}

// Label finds one of the organization's labels; it takes issues.read.
func (s *Service) Label(ctx context.Context, v Viewer, orgID, labelID uuid.UUID) (Label, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Label")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.String("cobranch.label.id", labelID.String()))
	if err := s.require(ctx, v, orgID, PermRead); err != nil {
		return Label{}, failed(span, "reading the label", err)
	}
	label, err := s.labels.Label(ctx, orgID, labelID)
	if err != nil {
		return Label{}, failed(span, "reading the label", err)
	}
	return label, nil
}

// CreateLabel creates a label in the organization; it takes issues.manage.
func (s *Service) CreateLabel(ctx context.Context, v Viewer, orgID uuid.UUID, in LabelInput) (Label, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.CreateLabel")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	if err := s.require(ctx, v, orgID, PermManage); err != nil {
		return Label{}, failed(span, "creating the label", err)
	}
	if in.Color == "" {
		in.Color = ColorGray
	}
	name, err := ValidateLabelName(in.Name)
	if err := errors.Join(err, ValidateColor(in.Color), ValidateLabelDescription(in.Description)); err != nil {
		return Label{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Label{}, fmt.Errorf("making an ID: %w", err)
	}
	now := s.now()
	label := Label{ID: id, OrgID: orgID, Name: name, Color: in.Color, Description: in.Description, CreatedAt: now, UpdatedAt: now, Version: 1}
	created, err := s.labels.CreateLabel(ctx, v.ID, label)
	if err != nil {
		return Label{}, failed(span, "creating the label", err)
	}
	return created, nil
}

// UpdateLabel changes the fields p names of one of the organization's labels; it takes issues.manage. A change that
// names a version the label no longer is gets ErrLabelChanged; one that changes nothing changes nothing.
func (s *Service) UpdateLabel(ctx context.Context, v Viewer, orgID, labelID uuid.UUID, p LabelPatch) (Label, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.UpdateLabel")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.String("cobranch.label.id", labelID.String()))
	if err := s.require(ctx, v, orgID, PermManage); err != nil {
		return Label{}, failed(span, "changing the label", err)
	}
	cur, err := s.labels.Label(ctx, orgID, labelID)
	if err != nil {
		return Label{}, failed(span, "changing the label", err)
	}
	if p.Version < 0 || Stale(p.Version, cur.Version) {
		return Label{}, ErrLabelChanged
	}
	next, err := patchLabel(cur, p)
	if err != nil {
		return Label{}, err
	}
	if next == cur {
		return cur, nil
	}
	updated, err := s.labels.UpdateLabel(ctx, v.ID, next, cur.Version)
	if err != nil {
		return Label{}, failed(span, "changing the label", err)
	}
	return updated, nil
}

// patchLabel is cur with p's fields, validated.
func patchLabel(cur Label, p LabelPatch) (Label, error) {
	next := cur
	var errs []error
	if p.Name != nil {
		name, err := ValidateLabelName(*p.Name)
		next.Name, errs = name, append(errs, err)
	}
	if p.Color != nil {
		next.Color, errs = *p.Color, append(errs, ValidateColor(*p.Color))
	}
	if p.Description != nil {
		next.Description, errs = *p.Description, append(errs, ValidateLabelDescription(*p.Description))
	}
	if err := errors.Join(errs...); err != nil {
		return Label{}, err
	}
	return next, nil
}

// DeleteLabel deletes one of the organization's labels, which takes it off its issues; it takes issues.manage. A
// deletion that names a version the label no longer is gets ErrLabelChanged.
func (s *Service) DeleteLabel(ctx context.Context, v Viewer, orgID, labelID uuid.UUID, version int64) error {
	ctx, span := tracer.Start(ctx, "issues.Service.DeleteLabel")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.String("cobranch.label.id", labelID.String()))
	if err := s.require(ctx, v, orgID, PermManage); err != nil {
		return failed(span, "deleting the label", err)
	}
	label, err := s.labels.Label(ctx, orgID, labelID)
	if err != nil {
		return failed(span, "deleting the label", err)
	}
	if version < 0 || Stale(version, label.Version) {
		return ErrLabelChanged
	}
	if err := s.labels.DeleteLabel(ctx, v.ID, label, label.Version); err != nil {
		return failed(span, "deleting the label", err)
	}
	return nil
}

// issueLabels are the organization's labels ids names, by name, for an issue to carry; a repeated one counts once,
// and one that isn't the organization's, or more than 20, is a *orgs.ValidationError.
func (s *Service) issueLabels(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]IssueLabel, error) {
	ids = distinct(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxIssueLabels {
		return nil, &orgs.ValidationError{Field: "labelIds", Code: "labels.invalid", Message: "an issue carries at most 20 labels"}
	}
	labels, err := s.labels.LabelsOf(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the labels: %w", err)
	}
	if len(labels) != len(ids) {
		return nil, &orgs.ValidationError{Field: "labelIds", Code: "labels.invalid", Message: "not labels of this organization"}
	}
	return labels, nil
}

// relabel is next carrying the labels ids names, and its change; next as it is for nil.
func (s *Service) relabel(ctx context.Context, cur, next Issue, changes []Change, ids *[]uuid.UUID) (Issue, []Change, error) {
	if ids == nil {
		return next, changes, nil
	}
	labels, err := s.issueLabels(ctx, cur.OrgID, *ids)
	if err != nil {
		return Issue{}, nil, err
	}
	next.Labels = labels
	return next, changed(changes, "labels", labelNames(cur.Labels), labelNames(labels)), nil
}

// labelNames are the labels' names as a change shows them: by name, comma-separated.
func labelNames(labels []IssueLabel) string {
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

// distinct are ids in order, each once.
func distinct(ids []uuid.UUID) []uuid.UUID {
	return slices.Compact(slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }))
}
