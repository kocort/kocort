package task

import "testing"

func TestBuildSessionsSpawnPlanDefaultsToSubagent(t *testing.T) {
	plan := BuildSessionsSpawnPlan(SessionsSpawnToolInput{
		Task: "do it",
	}, SubagentSpawnDefaults{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
	})
	if plan.Runtime != SpawnRuntimeSubagent {
		t.Fatalf("expected subagent runtime, got %+v", plan)
	}
	if plan.SubagentSpawn.Task != "do it" || plan.SubagentSpawn.RequesterAgentID != "main" {
		t.Fatalf("unexpected subagent request: %+v", plan.SubagentSpawn)
	}
}

func TestBuildSessionsSpawnPlanPreservesACPRuntime(t *testing.T) {
	plan := BuildSessionsSpawnPlan(SessionsSpawnToolInput{
		Task:    "do it",
		Runtime: "acp",
	}, SubagentSpawnDefaults{})
	if plan.Runtime != SpawnRuntimeACP {
		t.Fatalf("expected acp runtime, got %+v", plan)
	}
	if plan.ACPSpawn.Task != "do it" || plan.ACPSpawn.SpawnMode != "run" {
		t.Fatalf("unexpected acp request: %+v", plan.ACPSpawn)
	}
}

func TestBuildSessionsSpawnPlanCarriesExplicitCompletionOptOut(t *testing.T) {
	plan := BuildSessionsSpawnPlan(SessionsSpawnToolInput{
		Task:                        "do it",
		ExpectsCompletionMessage:    false,
		ExpectsCompletionMessageSet: true,
	}, SubagentSpawnDefaults{})
	if plan.SubagentSpawn.ExpectsCompletionMessage || !plan.SubagentSpawn.ExpectsCompletionMessageSet {
		t.Fatalf("expected explicit completion opt-out to be preserved, got %+v", plan.SubagentSpawn)
	}
}
