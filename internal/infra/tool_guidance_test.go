package infra_test

import (
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/tool"
)

func TestBuildToolGuidanceSectionMentionsCronForReminders(t *testing.T) {
	text := infra.BuildToolGuidanceSection([]infra.PromptTool{tool.NewCronTool()})
	if !strings.Contains(text, "use cron instead of only promising to remember") {
		t.Fatalf("expected cron reminder guidance, got %s", text)
	}
	if !strings.Contains(text, "continue the turn normally and confirm the plan in your own words") {
		t.Fatalf("expected cron post-tool confirmation guidance, got %s", text)
	}
}
