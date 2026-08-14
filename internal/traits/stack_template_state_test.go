package traits

import (
	"encoding/json"
	"testing"
)

// The nine cells of the state grid, as the two comparisons that produce them.
func TestStackTemplatePlanAndLiveState(t *testing.T) {
	t.Parallel()

	desired := func(stackTemplate StackTemplate) StackTemplate {
		stackTemplate.DesiredTemplateRevisionID = TemplateRevisionID("rev_2")
		stackTemplate.DesiredConfigJSON = json.RawMessage(`{"region":"us-east-1"}`)
		return stackTemplate
	}

	tests := []struct {
		name          string
		stackTemplate StackTemplate
		wantPlan      PlanState
		wantLive      LiveState
	}{
		{
			name:          "fresh install has neither a plan nor an apply",
			stackTemplate: desired(StackTemplate{}),
			wantPlan:      PlanNone,
			wantLive:      LiveNever,
		},
		{
			name: "plan matching desired is safe to apply",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			}),
			wantPlan: PlanMatches,
			wantLive: LiveNever,
		},
		{
			name: "config edited after the plan makes it stale",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
			}),
			wantPlan: PlanStale,
			wantLive: LiveNever,
		},
		{
			name: "revision changed after the plan makes it stale even with an identical config",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_1"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			}),
			wantPlan: PlanStale,
			wantLive: LiveNever,
		},
		{
			name: "steady state: plan and live both match desired",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
				LastAppliedRunID:              TemplateRunID("run_apply_1"),
				LastAppliedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastAppliedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			}),
			wantPlan: PlanMatches,
			wantLive: LiveMatches,
		},
		{
			name: "edited after going live: the worst cell",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
				LastAppliedRunID:              TemplateRunID("run_apply_1"),
				LastAppliedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastAppliedConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
			}),
			wantPlan: PlanStale,
			wantLive: LiveDiffers,
		},
		{
			name: "a recorded run with no config fails closed",
			stackTemplate: desired(StackTemplate{
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastAppliedRunID:              TemplateRunID("run_apply_1"),
				LastAppliedTemplateRevisionID: TemplateRevisionID("rev_2"),
			}),
			wantPlan: PlanStale,
			wantLive: LiveDiffers,
		},
		{
			name: "key order is not a difference",
			stackTemplate: func() StackTemplate {
				stackTemplate := StackTemplate{
					DesiredTemplateRevisionID:     TemplateRevisionID("rev_2"),
					DesiredConfigJSON:             json.RawMessage(`{"aa":1,"b":2}`),
					LastPlannedRunID:              TemplateRunID("run_plan_1"),
					LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
					// What Postgres hands back: jsonb orders keys by length
					// first, so this is byte-unequal to desired and
					// semantically identical.
					LastPlannedConfigJSON: json.RawMessage(`{"b": 2, "aa": 1}`),
				}
				return stackTemplate
			}(),
			wantPlan: PlanMatches,
			wantLive: LiveNever,
		},
		{
			name: "an all-optional template compares empty against empty",
			stackTemplate: StackTemplate{
				DesiredTemplateRevisionID:     TemplateRevisionID("rev_2"),
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{}`),
			},
			wantPlan: PlanMatches,
			wantLive: LiveNever,
		},
		{
			// The install-time config is history, not a second source of desired
			// state. A plan matching it but not desired is stale.
			name: "the install-time config is not consulted",
			stackTemplate: StackTemplate{
				DesiredTemplateRevisionID:     TemplateRevisionID("rev_2"),
				InstalledConfigJSON:           json.RawMessage(`{"region":"us-east-1"}`),
				LastPlannedRunID:              TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: TemplateRevisionID("rev_2"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			},
			wantPlan: PlanStale,
			wantLive: LiveNever,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.stackTemplate.PlanState(); got != test.wantPlan {
				t.Fatalf("PlanState() = %q, want %q", got, test.wantPlan)
			}
			if got := test.stackTemplate.LiveState(); got != test.wantLive {
				t.Fatalf("LiveState() = %q, want %q", got, test.wantLive)
			}
		})
	}
}

func TestStackTemplateDesiredConfigIgnoresTheInstallTimeConfig(t *testing.T) {
	t.Parallel()

	stackTemplate := StackTemplate{
		InstalledConfigJSON: json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:   json.RawMessage(`{"region":"eu-west-1"}`),
	}
	if got := string(stackTemplate.DesiredConfig()); got != `{"region":"eu-west-1"}` {
		t.Fatalf("DesiredConfig() = %s, want the desired config", got)
	}

	// An empty desired config means the config is empty, not absent. The column
	// is not null, so a persisted row can never say otherwise, and treating the
	// install-time config as a fallback would resurrect the ambiguity.
	stackTemplate.DesiredConfigJSON = nil
	if got := string(stackTemplate.DesiredConfig()); got != `{}` {
		t.Fatalf("DesiredConfig() = %s, want an empty object", got)
	}
}
