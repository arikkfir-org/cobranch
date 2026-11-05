package agents

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// USD is an amount of US dollars, in millionths: 1000000 is $1. Prices, caps and what tasks spend are all USD.
type USD int64

// Bounds of the amounts the organization sets.
const (
	// MaxPrice is the most a model may cost per million tokens: $10000.
	MaxPrice USD = 10_000_000_000
	// MaxCap is the most a cap may allow a task to spend: $1000000.
	MaxCap USD = 1_000_000_000_000
	// MaxTokens is the most tokens one reported call may have read or written.
	MaxTokens = 100_000_000
)

// ParseUSD reads an amount of dollars written as a decimal number, such as 8, 8.00 or 0.000150: at most six digits
// after the point, never negative.
func ParseUSD(s string) (USD, error) {
	whole, fraction, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(s), "$"), ".")
	digits := func(d string) bool { return strings.Trim(d, "0123456789") == "" }
	if whole == "" || len(fraction) > 6 || !digits(whole) || !digits(fraction) || len(whole) > 12 {
		return 0, fmt.Errorf("%q is not an amount of dollars such as 8.00, with at most six digits after the point", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading %q: %w", s, err)
	}
	f, err := strconv.ParseInt((fraction + "000000")[:6], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading %q: %w", s, err)
	}
	return USD(w*1_000_000 + f), nil
}

// String writes the amount as dollars with at least two digits after the point and none it doesn't need past them:
// 8.00, 0.42, 0.00015.
func (u USD) String() string {
	sign := ""
	if u < 0 {
		sign, u = "-", -u
	}
	fraction := strings.TrimRight(fmt.Sprintf("%06d", int64(u%1_000_000)), "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	return sign + strconv.FormatInt(int64(u/1_000_000), 10) + "." + fraction
}

// Model is one of the organization's models, of a provider it added, which its agents may run on: what the
// organization pays for the tokens it reads and writes, from which Cobranch prices each call of it. What a task may
// spend is its agent's to cap.
type Model struct {
	OrgID    uuid.UUID
	Provider string
	Name     string
	// InputPrice and OutputPrice are what a million tokens the model reads, and writes, cost.
	InputPrice  USD
	OutputPrice USD
	// Default is whether a new agent starts on it; the organization has at most one.
	Default bool
	// Agents are the agents that run on it, by name.
	Agents []Principal
	// CreatedBy is who added it; nil for one Cobranch added for the agents already on it, and once its adder is gone.
	CreatedBy *Principal
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts its changes from 1: it is its ETag.
	Version int64
}

// Cost is what a call that read input tokens and wrote output tokens costs at the model's prices, rounded up to the
// next millionth of a dollar.
func (m Model) Cost(input, output int64) USD {
	total := new(big.Int).Mul(big.NewInt(input), big.NewInt(int64(m.InputPrice)))
	total.Add(total, new(big.Int).Mul(big.NewInt(output), big.NewInt(int64(m.OutputPrice))))
	cost, rest := total.QuoRem(total, big.NewInt(1_000_000), new(big.Int))
	if rest.Sign() > 0 {
		cost.Add(cost, big.NewInt(1))
	}
	if !cost.IsInt64() {
		return USD(math.MaxInt64)
	}
	return USD(cost.Int64())
}

// ModelInput is what the organization sets on one of its models.
type ModelInput struct {
	InputPrice  USD
	OutputPrice USD
	Default     bool
}

// The model's own errors.
var (
	// ErrModelInUse is a model removed while agents run on it.
	ErrModelInUse = errors.New("agents run on this model")
)

// ValidateModelInput refuses what a model is set to unless each price is at most MaxPrice.
func ValidateModelInput(in ModelInput) error {
	if in.InputPrice < 0 || in.InputPrice > MaxPrice || in.OutputPrice < 0 || in.OutputPrice > MaxPrice {
		return invalid("inputPrice", "model.price_invalid", "a price is $0 to $10000 per million tokens")
	}
	return nil
}

// ValidateCap refuses an agent's cap unless it is at most MaxCap; 0 is none.
func ValidateCap(c USD) error {
	if c < 0 || c > MaxCap {
		return invalid("cap", "agent.cap_invalid", "a cap is $0.000001 to $1000000 a task, or none")
	}
	return nil
}

// Usage is a call of a task's model, as the model proxy of its pod reports it: an ID of its own, so a report it repeats
// counts once, the model it asked, and the tokens the model read and wrote.
type Usage struct {
	ID     uuid.UUID
	Model  string
	Input  int64
	Output int64
}

// ValidateUsage refuses a report without an ID or a model, or with tokens below 0 or above MaxTokens.
func ValidateUsage(u Usage) error {
	switch {
	case u.ID == uuid.Nil:
		return invalid("id", "spend.id_invalid", "a report names its call by an ID of its own")
	case strings.TrimSpace(u.Model) == "":
		return invalid("model", "spend.model_invalid", "a report names the model it asked")
	case u.Input < 0 || u.Output < 0 || u.Input > MaxTokens || u.Output > MaxTokens:
		return invalid("inputTokens", "spend.tokens_invalid", "a call reads and writes 0 to 100000000 tokens")
	}
	return nil
}

// Spend is what a task spent so far: in dollars, at its organization's prices, and in tokens; the most it may spend, 0
// for none; and whether it has ended, as it does once it reaches that cap.
type Spend struct {
	Spent  USD
	Input  int64
	Output int64
	Cap    USD
	Ended  bool
}

// CapReason is why a task that reached its cap failed.
func CapReason(spent, limit USD) string {
	return fmt.Sprintf("It spent $%s, reaching its cap of $%s a task.", spent, limit)
}

// Models lists the organization's models by provider and name, each with the agents on it; it takes org.read.
func (s *Service) Models(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Model, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Models", onOrg(orgID))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return nil, failed(span, "listing the models", err)
	}
	list, err := s.store.Models(ctx, orgID)
	return list, failed(span, "listing the models", err)
}

// Model finds one of the organization's models, with the agents on it; it takes org.read.
func (s *Service) Model(ctx context.Context, v Viewer, orgID uuid.UUID, provider, name string) (Model, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Model", onModel(orgID, provider, name))
	defer span.End()
	if _, err := s.held(ctx, v, orgID); err != nil {
		return Model{}, failed(span, "finding the model", err)
	}
	m, err := s.store.Model(ctx, orgID, provider, name)
	return m, failed(span, "finding the model", err)
}

// PutModel adds a model of a provider the organization added to its models, or changes one of them, and answers it and
// whether it added it; it takes org.manage. Making it the default makes the one before it no longer so. A version of
// NoVersion only adds one, AnyVersion changes the one there, and any other changes it at that version: ErrChanged
// otherwise.
func (s *Service) PutModel(ctx context.Context, v Viewer, orgID uuid.UUID, provider, name string, in ModelInput, version int64) (Model, bool, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.PutModel", onModel(orgID, provider, name))
	defer span.End()
	if _, err := s.require(ctx, v, orgID, orgs.PermManage); err != nil {
		return Model{}, false, failed(span, "setting the model", err)
	}
	provider, name = strings.TrimSpace(provider), strings.TrimSpace(name)
	err := ValidateModelInput(in)
	if _, _, invalidName := ValidateModel(provider, name); invalidName != nil || provider == "" {
		err = invalid("model", "model.name_invalid", "a model is named, with its provider, in 1 to 200 characters on one line")
	}
	if err != nil {
		return Model{}, false, failed(span, "setting the model", err)
	}
	m, created, err := s.store.PutModel(ctx, v.ID, orgID, provider, name, in, version)
	return m, created, failed(span, "setting the model", err)
}

// RemoveModel removes one of the organization's models, at version or at any version for AnyVersion; it takes
// org.manage, and answers ErrModelInUse while agents run on it.
func (s *Service) RemoveModel(ctx context.Context, v Viewer, orgID uuid.UUID, provider, name string, version int64) error {
	ctx, span := tracer.Start(ctx, "agents.Service.RemoveModel", onModel(orgID, provider, name))
	defer span.End()
	if _, err := s.require(ctx, v, orgID, orgs.PermManage); err != nil {
		return failed(span, "removing the model", err)
	}
	return failed(span, "removing the model", s.store.RemoveModel(ctx, v.ID, orgID, provider, name, version))
}

// RecordSpend records a call of the model of the task the request's task token works, as the model proxy of its pod
// reports it, priced at the organization's prices for that model of its agent's provider, or for its agent's model when
// the organization has no prices for the one reported, and answers what the task spent. A task that reaches its cap
// ends Failed, saying so, and its token acts no more. A report that repeats an ID records nothing. Only a task token
// reports, and only while its task runs or waits: auth.ErrForbidden otherwise, or ErrEnded.
func (s *Service) RecordSpend(ctx context.Context, v Viewer, u Usage) (Spend, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.RecordSpend", trace.WithAttributes(attribute.String("cobranch.spend.id", u.ID.String()),
		attribute.String("cobranch.spend.model", u.Model), attribute.Int64("cobranch.spend.input_tokens", u.Input),
		attribute.Int64("cobranch.spend.output_tokens", u.Output)))
	defer span.End()
	id, ok := working(ctx, v)
	if !ok {
		return Spend{}, failed(span, "recording the task's spend", fmt.Errorf("%w: only a task token reports what its task spent", auth.ErrForbidden))
	}
	if err := ValidateUsage(u); err != nil {
		return Spend{}, failed(span, "recording the task's spend", err)
	}
	span.SetAttributes(attribute.String("cobranch.task.id", id.String()))
	spend, err := s.store.RecordSpend(ctx, id, u)
	if err != nil {
		return Spend{}, failed(span, "recording the task's spend", err)
	}
	span.SetAttributes(attribute.Int64("cobranch.task.spent", int64(spend.Spent)), attribute.Bool("cobranch.task.ended", spend.Ended))
	return spend, nil
}

// onModel is a span's attributes naming one of the organization's models.
func onModel(orgID uuid.UUID, provider, name string) trace.SpanStartOption {
	return onOrg(orgID, attribute.String("cobranch.model_provider", provider), attribute.String("cobranch.model", name))
}
