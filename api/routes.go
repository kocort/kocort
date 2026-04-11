package api

import (
	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/api/handlers"
)

func (s *Server) registerRoutes(engine *gin.Engine) {
	// Initialize handler groups
	workspace := &handlers.Workspace{Runtime: s.Runtime}
	engineHandler := &handlers.Engine{Runtime: s.Runtime}
	system := &handlers.System{Runtime: s.Runtime}
	channels := &handlers.Channels{Runtime: s.Runtime}
	rpc := &handlers.RPC{
		Runtime: s.Runtime,
		WebchatStyle: handlers.WebchatStyle{
			Enabled: s.webchatEnabled(),
		},
	}
	events := &handlers.Events{Runtime: s.Runtime}

	// workspace routes
	engine.GET("/api/workspace/chat/bootstrap", workspace.ChatBootstrap)
	engine.POST("/api/workspace/chat/send", workspace.ChatSend)
	engine.POST("/api/workspace/chat/cancel", workspace.ChatCancel)
	engine.GET("/api/workspace/chat/history", workspace.ChatHistory)
	engine.GET("/api/workspace/chat/events", events.Serve)
	engine.GET("/api/workspace/media", workspace.Media)
	engine.GET("/api/workspace/tasks", workspace.TasksGet)
	engine.POST("/api/workspace/tasks", workspace.TasksCreate)
	engine.POST("/api/workspace/tasks/update", workspace.TaskUpdate)
	engine.POST("/api/workspace/tasks/delete", workspace.TaskDelete)
	engine.POST("/api/workspace/tasks/cancel", workspace.TaskCancel)

	// engine routes
	engine.GET("/api/engine/brain", engineHandler.Brain)
	engine.POST("/api/engine/brain/save", engineHandler.BrainSave)
	engine.POST("/api/engine/brain/models/upsert", engineHandler.BrainModelUpsert)
	engine.POST("/api/engine/brain/models/delete", engineHandler.BrainModelDelete)
	engine.POST("/api/engine/brain/models/default", engineHandler.BrainModelSetDefault)
	engine.POST("/api/engine/brain/models/fallback", engineHandler.BrainModelSetFallback)
	engine.POST("/api/engine/brain/cerebellum/start", engineHandler.CerebellumStart)
	engine.POST("/api/engine/brain/cerebellum/stop", engineHandler.CerebellumStop)
	engine.POST("/api/engine/brain/cerebellum/restart", engineHandler.CerebellumRestart)
	engine.POST("/api/engine/brain/cerebellum/model", engineHandler.CerebellumSelectModel)
	engine.POST("/api/engine/brain/cerebellum/model/clear", engineHandler.CerebellumClearModelSelection)
	engine.POST("/api/engine/brain/cerebellum/model/delete", engineHandler.CerebellumDeleteModel)
	engine.POST("/api/engine/brain/cerebellum/download", engineHandler.CerebellumDownloadModel)
	engine.POST("/api/engine/brain/cerebellum/download/cancel", engineHandler.CerebellumCancelDownload)
	engine.POST("/api/engine/brain/cerebellum/download/cancel-lib", engineHandler.CerebellumCancelLibDownload)
	engine.POST("/api/engine/brain/cerebellum/help", engineHandler.CerebellumHelp)
	engine.POST("/api/engine/brain/mode", engineHandler.BrainModeSwitch)
	engine.POST("/api/engine/brain/local/start", engineHandler.BrainLocalStart)
	engine.POST("/api/engine/brain/local/stop", engineHandler.BrainLocalStop)
	engine.POST("/api/engine/brain/local/restart", engineHandler.BrainLocalRestart)
	engine.POST("/api/engine/brain/local/model", engineHandler.BrainLocalSelectModel)
	engine.POST("/api/engine/brain/local/model/clear", engineHandler.BrainLocalClearModelSelection)
	engine.POST("/api/engine/brain/local/model/delete", engineHandler.BrainLocalDeleteModel)
	engine.POST("/api/engine/brain/local/download", engineHandler.BrainLocalDownloadModel)
	engine.POST("/api/engine/brain/local/download/cancel", engineHandler.BrainLocalCancelDownload)
	engine.POST("/api/engine/brain/local/download/cancel-lib", engineHandler.BrainLocalCancelLibDownload)
	engine.POST("/api/engine/brain/local/params", engineHandler.BrainLocalUpdateParams)
	engine.POST("/api/engine/brain/cerebellum/params", engineHandler.CerebellumUpdateParams)
	engine.POST("/api/engine/brain/oauth/start", engineHandler.OAuthDeviceCodeStart)
	engine.POST("/api/engine/brain/oauth/poll", engineHandler.OAuthDeviceCodePoll)
	engine.GET("/api/engine/brain/oauth/status", engineHandler.OAuthStatus)
	engine.POST("/api/engine/brain/oauth/logout", engineHandler.OAuthLogout)
	engine.GET("/api/engine/capabilities", engineHandler.Capabilities)
	engine.POST("/api/engine/capabilities/save", engineHandler.CapabilitiesSave)
	engine.GET("/api/engine/capabilities/skill/files", engineHandler.SkillFiles)
	engine.GET("/api/engine/capabilities/skill/file", engineHandler.SkillFileRead)
	engine.POST("/api/engine/capabilities/skill/install", engineHandler.SkillInstall)
	engine.POST("/api/engine/capabilities/skill/import/validate", engineHandler.SkillImportValidate)
	engine.POST("/api/engine/capabilities/skill/import/confirm", engineHandler.SkillImportConfirm)
	engine.POST("/api/engine/browse-dir", engineHandler.BrowseDir)
	engine.POST("/api/engine/capabilities/skill/browse-dir", engineHandler.SkillBrowseDir)
	engine.GET("/api/engine/data", engineHandler.Data)
	engine.POST("/api/engine/data/save", engineHandler.DataSave)
	engine.GET("/api/engine/sandbox", engineHandler.Sandbox)
	engine.POST("/api/engine/sandbox/save", engineHandler.SandboxSave)

	// integration routes
	engine.GET("/api/integrations/channels", channels.List)
	engine.POST("/api/integrations/channels/save", channels.Save)
	engine.POST("/api/integrations/channels/weixin/qr/start", channels.WeixinQRStart)
	engine.POST("/api/integrations/channels/weixin/qr/poll", channels.WeixinQRPoll)

	// system routes
	engine.GET("/api/system/dashboard", system.Dashboard)
	engine.POST("/api/system/audit/list", system.AuditList)
	engine.GET("/api/system/environment", system.Environment)
	engine.POST("/api/system/environment/save", system.EnvironmentSave)
	engine.POST("/api/system/environment/reload", system.EnvironmentReload)
	engine.GET("/api/system/network", system.Network)
	engine.POST("/api/system/network/save", system.NetworkSave)
	engine.GET("/api/system/llamacpp", system.LlamaCpp)
	engine.POST("/api/system/llamacpp/save", system.LlamaCppSave)
	engine.POST("/api/system/llamacpp/download", system.LlamaCppDownload)
	engine.POST("/api/system/llamacpp/download/cancel", system.LlamaCppDownloadCancel)

	// setup / onboarding routes
	engine.GET("/api/setup/status", engineHandler.SetupStatus)
	engine.POST("/api/setup/complete", engineHandler.SetupComplete)

	// rpc routes
	engine.GET("/healthz", rpc.Health)
	engine.POST("/rpc/chat.send", rpc.ChatSend)
	engine.POST("/rpc/chat.cancel", rpc.ChatCancel)
	engine.GET("/rpc/chat.history", rpc.ChatHistory)
	engine.GET("/rpc/chat.events", events.Serve)
	engine.GET("/rpc/dashboard.snapshot", rpc.DashboardSnapshot)
	engine.POST("/rpc/audit.list", rpc.AuditList)

	// channel inbound
	engine.POST("/channels/:channelID", rpc.ChannelInbound)

	// webchat
	if s.webchatEnabled() {
		s.registerStaticRoutes(engine)
	}
}
