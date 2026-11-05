package rest

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// onSpend is a path with Scout on a-model, capped at $8, and its first two tasks running: 1, anywhere, has spent $0.30
// and 2, on website, $3.00.
func onSpend(t *testing.T, path string) func(*orgFixture) string {
	return onModel(t, func(f *orgFixture) string {
		for number, input := range map[int64]int64{1: 100_000, 2: 1_000_000} {
			f.agentStore.SetState(f.acme.ID, number, agents.TaskRunning)
			task, err := f.agentStore.Task(t.Context(), f.acme.ID, number)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.agentStore.RecordSpend(t.Context(), task.ID, agents.Usage{ID: uuid.New(), Model: "a-model", Input: input}); err != nil {
				t.Fatal(err)
			}
		}
		return path
	})
}

// wantSpend checks that the answer is October 2026's in zone from since, with Scout's three tasks, two running, that
// spent $3.30, the costliest the one named, and the Reviewer's none, and a-model's two tasks.
func wantSpend(zone, since string, costliest *TaskCost) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, f *orgFixture, body []byte) {
		t.Helper()
		got := decode[OrganizationSpend](t, body)
		if got.Month != "2026-10" || got.Zone != zone || got.Since.Format(time.RFC3339) != since || len(got.Agents) != 2 || len(got.Models) != 1 {
			t.Fatalf("spend = %s", body)
		}
		reviewer, scout := got.Agents[0], got.Agents[1]
		if reviewer.Agent.DisplayName != "Reviewer" || reviewer.Tasks != 0 || reviewer.Spent != "0.00" || reviewer.Cap != nil || reviewer.Costliest != nil {
			t.Errorf("Reviewer's = %+v", reviewer)
		}
		if scout.Agent.Id != f.scout.ID || scout.Provider == nil || *scout.Provider != "anthropic" || scout.Model == nil || *scout.Model != "a-model" ||
			scout.Cap == nil || *scout.Cap != "8.00" || scout.Tasks != 3 || scout.Active != 2 || scout.FailedOnCap != 0 || scout.Spent != "3.30" ||
			(scout.Costliest == nil) != (costliest == nil) || (costliest != nil && *scout.Costliest != *costliest) {
			t.Errorf("Scout's = %s", body)
		}
		if got.Models[0] != (ModelSpend{Provider: "anthropic", Model: "a-model", Tasks: 2, InputTokens: 1_100_000, Spent: "3.30"}) {
			t.Errorf("models = %+v", got.Models)
		}
	}
}

func TestOrganizationSpend(t *testing.T) {
	const october = "/v1/orgs/acme/spend?month=2026-10"
	runOrgCases(t, []orgCase{
		{name: "an Owner's names the costliest task", viewer: olivia, method: "GET", path: onSpend(t, october), wantStatus: 200,
			check: wantSpend("UTC", "2026-10-01T00:00:00Z", &TaskCost{Number: 2, Title: "Fix the login", Spent: "3.00"})},
		{name: "a Member's names only one they see", viewer: maxim, method: "GET", path: onSpend(t, october), wantStatus: 200,
			check: wantSpend("UTC", "2026-10-01T00:00:00Z", &TaskCost{Number: 1, Title: "Tidy up", Spent: "0.30"})},
		{name: "in a zone", viewer: olivia, method: "GET", path: onSpend(t, october+"&zone=Asia/Jerusalem"), wantStatus: 200,
			check: wantSpend("Asia/Jerusalem", "2026-09-30T21:00:00Z", &TaskCost{Number: 2, Title: "Fix the login", Spent: "3.00"})},
		{name: "a month without tasks", viewer: olivia, method: "GET", path: onSpend(t, "/v1/orgs/acme/spend?month=2026-09"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[OrganizationSpend](t, body)
				if len(got.Agents) != 2 || got.Agents[1].Tasks != 0 || got.Agents[1].Spent != "0.00" || got.Agents[1].Costliest != nil ||
					len(got.Models) != 1 || got.Models[0].Tasks != 0 || got.Models[0].Spent != "0.00" {
					t.Errorf("spend = %s", body)
				}
			}},
		{name: "this month in UTC by default", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/spend"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[OrganizationSpend](t, body); got.Zone != "UTC" || got.Month != time.Now().UTC().Format("2006-01") ||
					!got.Until.Equal(got.Since.AddDate(0, 1, 0)) || got.Agents == nil || got.Models == nil {
					t.Errorf("spend = %s", body)
				}
			}},
		{name: "what the caller holds", viewer: maxim, method: "GET", path: fixed(october), wantStatus: 304,
			headerFor: func(f *orgFixture) http.Header { onSpend(t, october)(f); return held(maxim, fixed(october))(f) }},
		{name: "what the caller held before the tasks spent more", viewer: maxim, method: "GET", path: fixed(october), wantStatus: 200,
			headerFor: func(f *orgFixture) http.Header {
				h := held(maxim, fixed(october))(f)
				onSpend(t, october)(f)
				return h
			}},
		{name: "a month that isn't one", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/spend?month=2026-13"), wantStatus: 400,
			wantCode: "spend.month_invalid"},
		{name: "a zone that isn't one", viewer: maxim, method: "GET", path: fixed(october + "&zone=Mars/Olympus"), wantStatus: 400,
			wantCode: "spend.zone_invalid"},
		{name: "as an outsider", viewer: nia, method: "GET", path: fixed(october), wantStatus: 404, wantCode: "organization.not_found"},
	})
}
