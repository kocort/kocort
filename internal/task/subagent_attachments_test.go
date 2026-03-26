package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeSubagentAttachments(t *testing.T) {
	workspace := t.TempDir()
	result, err := MaterializeSubagentAttachments(workspace, []SubagentInlineAttachment{
		{Name: "a.txt", Content: "hello", Encoding: "utf8"},
	}, "docs")
	if err != nil {
		t.Fatalf("materialize attachments: %v", err)
	}
	if result == nil || result.Receipt == nil || result.Receipt.Count != 1 {
		t.Fatalf("unexpected attachment result: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(result.AbsDir, "a.txt"))
	if err != nil {
		t.Fatalf("read attachment file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected attachment contents: %q", string(data))
	}
	if !strings.Contains(result.SystemPromptSuffix, result.Receipt.RelDir) {
		t.Fatalf("expected prompt suffix to mention rel dir, got %q", result.SystemPromptSuffix)
	}
}

func TestMaterializeSubagentAttachmentsRejectsInvalidName(t *testing.T) {
	_, err := MaterializeSubagentAttachments(t.TempDir(), []SubagentInlineAttachment{
		{Name: "../bad", Content: "hello"},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "attachments_invalid_name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
}

func TestCleanupMaterializedSubagentAttachments(t *testing.T) {
	workspace := t.TempDir()
	result, err := MaterializeSubagentAttachments(workspace, []SubagentInlineAttachment{
		{Name: "a.txt", Content: "hello"},
	}, "")
	if err != nil {
		t.Fatalf("materialize attachments: %v", err)
	}
	CleanupMaterializedSubagentAttachments(result.AbsDir, result.RootDir)
	if _, statErr := os.Stat(result.AbsDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected attachment dir removed, got %v", statErr)
	}
}

func TestMaterializeSubagentAttachmentsDefaultsToNonRetainedOnKeep(t *testing.T) {
	result, err := MaterializeSubagentAttachments(t.TempDir(), []SubagentInlineAttachment{
		{Name: "a.txt", Content: "hello"},
	}, "")
	if err != nil {
		t.Fatalf("materialize attachments: %v", err)
	}
	if result == nil || result.RetainOnSessionKeep {
		t.Fatalf("expected retain-on-keep default false, got %+v", result)
	}
}

func TestMaterializeSubagentAttachmentsWithPolicy(t *testing.T) {
	result, err := MaterializeSubagentAttachmentsWithPolicy(t.TempDir(), []SubagentInlineAttachment{
		{Name: "a.txt", Content: "hello"},
	}, "", SubagentAttachmentPolicy{
		MaxFiles:            1,
		MaxFileBytes:        16,
		MaxTotalBytes:       16,
		RetainOnSessionKeep: true,
	})
	if err != nil {
		t.Fatalf("materialize attachments with policy: %v", err)
	}
	if result == nil || !result.RetainOnSessionKeep {
		t.Fatalf("expected retain-on-keep from policy, got %+v", result)
	}
}

func TestMarkCompletionMessageSentCleansAttachmentsWhenKeepWithoutRetention(t *testing.T) {
	workspace := t.TempDir()
	result, err := MaterializeSubagentAttachments(workspace, []SubagentInlineAttachment{
		{Name: "a.txt", Content: "hello"},
	}, "")
	if err != nil {
		t.Fatalf("materialize attachments: %v", err)
	}
	registry := NewSubagentRegistry()
	registry.Register(SubagentRunRecord{
		RunID:                   "run-1",
		ChildSessionKey:         "agent:worker:subagent:one",
		RequesterSessionKey:     "agent:main:main",
		Cleanup:                 "keep",
		AttachmentsDir:          result.AbsDir,
		AttachmentsRootDir:      result.RootDir,
		RetainAttachmentsOnKeep: false,
	})
	registry.MarkCompletionMessageSent("run-1")
	if _, statErr := os.Stat(result.AbsDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected attachment dir removed after keep cleanup, got %v", statErr)
	}
}
