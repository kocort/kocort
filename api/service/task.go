package service

// Task request mapping functions.

import (
	"strings"

	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/task"
)

// TaskScheduleRequestFromCreate converts TaskCreateRequest to task.TaskScheduleRequest.
func TaskScheduleRequestFromCreate(req types.TaskCreateRequest) task.TaskScheduleRequest {
	return task.TaskScheduleRequest{
		AgentID:                req.AgentID,
		SessionKey:             req.SessionKey,
		Title:                  req.Title,
		Message:                req.Message,
		Channel:                req.Channel,
		To:                     req.To,
		AccountID:              req.AccountID,
		ThreadID:               req.ThreadID,
		Deliver:                req.Deliver,
		DeliveryMode:           req.DeliveryMode,
		DeliveryBestEffort:     req.DeliveryBestEffort,
		PayloadKind:            core.TaskPayloadKind(strings.TrimSpace(req.PayloadKind)),
		SessionTarget:          core.TaskSessionTarget(strings.TrimSpace(req.SessionTarget)),
		WakeMode:               core.TaskWakeMode(strings.TrimSpace(req.WakeMode)),
		FailureAlertAfter:      req.FailureAlertAfter,
		FailureAlertCooldownMs: req.FailureAlertCooldownMs,
		FailureAlertChannel:    req.FailureAlertChannel,
		FailureAlertTo:         req.FailureAlertTo,
		FailureAlertAccountID:  req.FailureAlertAccountID,
		FailureAlertMode:       req.FailureAlertMode,
		ScheduleKind:           core.TaskScheduleKind(strings.TrimSpace(req.ScheduleKind)),
		ScheduleAt:             req.ScheduleAt,
		ScheduleEveryMs:        req.ScheduleEveryMs,
		ScheduleAnchorMs:       req.ScheduleAnchorMs,
		ScheduleExpr:           req.ScheduleExpr,
		ScheduleTZ:             req.ScheduleTZ,
		ScheduleStaggerMs:      req.ScheduleStaggerMs,
		IntervalSeconds:        req.IntervalSeconds,
		RunAt:                  req.RunAt,
	}
}

// TaskScheduleRequestFromUpdate converts TaskUpdateRequest to task.TaskScheduleRequest.
func TaskScheduleRequestFromUpdate(req types.TaskUpdateRequest) task.TaskScheduleRequest {
	return task.TaskScheduleRequest{
		AgentID:                req.AgentID,
		SessionKey:             req.SessionKey,
		Title:                  req.Title,
		Message:                req.Message,
		Channel:                req.Channel,
		To:                     req.To,
		AccountID:              req.AccountID,
		ThreadID:               req.ThreadID,
		Deliver:                req.Deliver,
		DeliveryMode:           req.DeliveryMode,
		DeliveryBestEffort:     req.DeliveryBestEffort,
		PayloadKind:            core.TaskPayloadKind(strings.TrimSpace(req.PayloadKind)),
		SessionTarget:          core.TaskSessionTarget(strings.TrimSpace(req.SessionTarget)),
		WakeMode:               core.TaskWakeMode(strings.TrimSpace(req.WakeMode)),
		FailureAlertAfter:      req.FailureAlertAfter,
		FailureAlertCooldownMs: req.FailureAlertCooldownMs,
		FailureAlertChannel:    req.FailureAlertChannel,
		FailureAlertTo:         req.FailureAlertTo,
		FailureAlertAccountID:  req.FailureAlertAccountID,
		FailureAlertMode:       req.FailureAlertMode,
		ScheduleKind:           core.TaskScheduleKind(strings.TrimSpace(req.ScheduleKind)),
		ScheduleAt:             req.ScheduleAt,
		ScheduleEveryMs:        req.ScheduleEveryMs,
		ScheduleAnchorMs:       req.ScheduleAnchorMs,
		ScheduleExpr:           req.ScheduleExpr,
		ScheduleTZ:             req.ScheduleTZ,
		ScheduleStaggerMs:      req.ScheduleStaggerMs,
		IntervalSeconds:        req.IntervalSeconds,
		RunAt:                  req.RunAt,
	}
}
