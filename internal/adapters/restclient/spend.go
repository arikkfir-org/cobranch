package restclient

import (
	"context"
	"fmt"
	"net/http"

	"cobranch.dev/internal/services/agents"
)

// RecordSpend reports a call of the task's model, as its pod's model proxy makes it, and answers what the task spent
// so far (models.Meter): agents.ErrEnded once the task has ended, and harness.ErrRefused once its token acts no more,
// as it does from then on. A repeat of the report counts once, so a retry after an answer that got lost is safe.
func (c *API) RecordSpend(ctx context.Context, u agents.Usage) (agents.Spend, error) {
	resp, err := c.api.RecordMyTaskSpendWithResponse(ctx, TaskUsage{Id: u.ID, Model: u.Model, InputTokens: u.Input, OutputTokens: u.Output})
	if err != nil {
		return agents.Spend{}, fmt.Errorf("reporting call %s: %w", u.ID, err)
	}
	if resp.StatusCode() == http.StatusConflict {
		return agents.Spend{}, fmt.Errorf("reporting call %s: %w", u.ID, agents.ErrEnded)
	}
	if resp.JSON200 == nil {
		return agents.Spend{}, refused(fmt.Sprintf("reporting call %s", u.ID), resp.StatusCode(), resp.Body)
	}
	s := resp.JSON200
	out := agents.Spend{Input: s.InputTokens, Output: s.OutputTokens, Ended: s.Ended}
	if out.Spent, err = agents.ParseUSD(s.Spent); err != nil {
		return agents.Spend{}, fmt.Errorf("reading what the task spent, %q: %w", s.Spent, err)
	}
	if s.Cap != nil {
		if out.Cap, err = agents.ParseUSD(*s.Cap); err != nil {
			return agents.Spend{}, fmt.Errorf("reading the task's cap, %q: %w", *s.Cap, err)
		}
	}
	return out, nil
}
