package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	gw "github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

type ChatGateway struct {
	Runtime *runtime.Runtime
}

func NewChatGateway(rt *runtime.Runtime) *ChatGateway {
	return &ChatGateway{Runtime: rt}
}

func (g *ChatGateway) DefaultMainSessionKey() string {
	if g == nil || g.Runtime == nil {
		return ""
	}
	return session.BuildMainSessionKeyWithMain(
		config.ResolveDefaultConfiguredAgentID(g.Runtime.Config),
		config.ResolveSessionMainKey(g.Runtime.Config),
	)
}

func (g *ChatGateway) Send(ctx context.Context, req core.ChatSendRequest) (core.ChatSendResponse, error) {
	if strings.TrimSpace(req.Channel) == "" {
		req.Channel = "webchat"
	}
	if strings.TrimSpace(req.To) == "" {
		req.To = "webchat-user"
	}
	return g.Runtime.ChatSend(ctx, req)
}

func (g *ChatGateway) Cancel(ctx context.Context, req core.ChatCancelRequest) (core.ChatCancelResponse, error) {
	if strings.TrimSpace(req.SessionKey) == "" {
		req.SessionKey = g.DefaultMainSessionKey()
	}
	return g.Runtime.ChatCancel(ctx, req)
}

func (g *ChatGateway) LoadHistory(sessionKey string, limit int, before int) core.ChatHistoryResponse {
	if g == nil || g.Runtime == nil || g.Runtime.Sessions == nil {
		return core.ChatHistoryResponse{}
	}
	history, total, hasMore, nextBefore, err := session.LoadChatHistoryPage(g.Runtime.Sessions, sessionKey, limit, before)
	if err != nil {
		return core.ChatHistoryResponse{}
	}
	sessionID := ""
	var skillsSnapshot *core.SkillSnapshotSummary
	if entry := g.Runtime.Sessions.Entry(sessionKey); entry != nil {
		sessionID = entry.SessionID
		skillsSnapshot = summarizeSkillSnapshot(entry.SkillsSnapshot)
	}
	return core.ChatHistoryResponse{
		SessionKey:     sessionKey,
		SessionID:      sessionID,
		SkillsSnapshot: skillsSnapshot,
		Messages:       history,
		Total:          total,
		HasMore:        hasMore,
		NextBefore:     nextBefore,
	}
}

func summarizeSkillSnapshot(snapshot *core.SkillSnapshot) *core.SkillSnapshotSummary {
	if snapshot == nil {
		return nil
	}
	skillNames := append([]string{}, snapshot.ResolvedName...)
	if len(skillNames) == 0 {
		skillNames = make([]string, 0, len(snapshot.Skills))
		for _, entry := range snapshot.Skills {
			if name := strings.TrimSpace(entry.Name); name != "" {
				skillNames = append(skillNames, name)
			}
		}
	}
	commandNames := make([]string, 0, len(snapshot.Commands))
	for _, command := range snapshot.Commands {
		if name := strings.TrimSpace(command.Name); name != "" {
			commandNames = append(commandNames, name)
		}
	}
	return &core.SkillSnapshotSummary{
		Version:      snapshot.Version,
		SkillNames:   skillNames,
		CommandNames: commandNames,
	}
}

func NormalizeChatRequestDefaults(req core.ChatSendRequest) core.ChatSendRequest {
	if strings.TrimSpace(req.Channel) == "" {
		req.Channel = "webchat"
	}
	if strings.TrimSpace(req.To) == "" {
		req.To = "webchat-user"
	}
	return req
}

func ParseHistoryWindow(r *http.Request) (int, int, error) {
	limit, err := gw.ParseChatHistoryLimit(r)
	if err != nil {
		return 0, 0, err
	}
	before, err := gw.ParseChatHistoryBefore(r)
	if err != nil {
		return 0, 0, err
	}
	return limit, before, nil
}
