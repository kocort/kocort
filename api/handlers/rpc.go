package handlers

// RPC HTTP handlers for legacy endpoints, channel inbound, health check, and webchat.

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	gw "github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

// RPC holds dependencies for RPC handlers.
type RPC struct {
	Runtime      *runtime.Runtime
	WebchatStyle WebchatStyle
}

// WebchatStyle configures webchat behavior.
type WebchatStyle struct {
	Enabled bool
}

// Health handles GET /healthz.
func (h *RPC) Health(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// ChatSend handles POST /rpc/chat.send.
func (h *RPC) ChatSend(c *gin.Context) {
	var req core.ChatSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Channel) == "" {
		req.Channel = "webchat"
	}
	if strings.TrimSpace(req.To) == "" {
		req.To = "webchat-user"
	}
	resp, err := h.Runtime.ChatSend(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ChatCancel handles POST /rpc/chat.cancel.
func (h *RPC) ChatCancel(c *gin.Context) {
	var req core.ChatCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SessionKey) == "" {
		req.SessionKey = session.BuildMainSessionKeyWithMain(
			config.ResolveDefaultConfiguredAgentID(h.Runtime.Config),
			config.ResolveSessionMainKey(h.Runtime.Config),
		)
	}
	resp, err := h.Runtime.ChatCancel(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ChatHistory handles GET /rpc/chat.history.
func (h *RPC) ChatHistory(c *gin.Context) {
	sessionKey := strings.TrimSpace(c.Query("sessionKey"))
	if sessionKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing sessionKey"})
		return
	}
	limit, err := gw.ParseChatHistoryLimit(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	before, err := gw.ParseChatHistoryBefore(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp := loadChatHistory(h.Runtime, sessionKey, limit, before)
	c.JSON(http.StatusOK, resp)
}

// DashboardSnapshot handles GET /rpc/dashboard.snapshot.
func (h *RPC) DashboardSnapshot(c *gin.Context) {
	snapshot, err := service.BuildDashboardSnapshot(c.Request.Context(), h.Runtime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

// AuditList handles POST /rpc/audit.list.
func (h *RPC) AuditList(c *gin.Context) {
	var req core.AuditListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.Runtime.Audit == nil {
		c.JSON(http.StatusOK, core.AuditListResponse{})
		return
	}
	events, err := h.Runtime.Audit.List(c.Request.Context(), core.AuditQuery{
		Category:   req.Category,
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
		TaskID:     req.TaskID,
		Limit:      req.Limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, core.AuditListResponse{Events: events})
}

// ChannelInbound handles POST /channels/:channelID.
func (h *RPC) ChannelInbound(c *gin.Context) {
	channelID := backend.NormalizeProviderID(c.Param("channelID"))
	if channelID == "" {
		c.String(http.StatusBadRequest, "missing channel id")
		return
	}
	transport := h.Runtime.Channels.GetChannel(channelID)
	if transport == nil {
		c.String(http.StatusNotImplemented, "channel transport not registered")
		return
	}
	if err := h.Runtime.Channels.EnsureStarted(c.Request.Context(), channelID, h.Runtime); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	transport.ServeHTTP(c.Writer, c.Request)
}

// Webchat handles GET /.
func (h *RPC) Webchat(c *gin.Context) {
	const page = `<!doctype html>
<html><head><meta charset="utf-8"><title>kocort webchat</title><style>
body{font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;max-width:1280px;margin:2rem auto;padding:0 1rem;background:#f6f1e8;color:#1f1a16}
.layout{display:grid;grid-template-columns:minmax(0,1fr) minmax(320px,420px);gap:1rem;align-items:start}
.panel{border:1px solid #c9b79c;background:#fffdf9;padding:1rem}
#log{min-height:320px;white-space:pre-wrap}
#debugLog{min-height:320px;max-height:70vh;overflow:auto;white-space:pre-wrap;background:#17120f;color:#f3e7d3;padding:1rem}
#debugStatus{margin:0 0 .75rem 0;color:#7a6851}
.debug-toolbar{display:flex;gap:.5rem;align-items:center;margin-bottom:.75rem}
textarea,input,button{font:inherit}
</style></head><body>
<h1>kocort webchat</h1>
<p>sessionKey <input id="sessionKey" value="agent:main:main" size="48"></p>
<div class="layout">
<div class="panel">
<div id="log"></div>
<p><textarea id="message" rows="4" cols="80"></textarea></p>
<p><button id="send">send</button></p>
</div>
<div class="panel">
<div class="debug-toolbar">
<strong>debug stream</strong>
<button id="clearDebug" type="button">clear</button>
</div>
<p id="debugStatus">waiting for events...</p>
<div id="debugLog"></div>
</div>
</div>
<script>
const log=document.getElementById('log');const debugLog=document.getElementById('debugLog');const debugStatus=document.getElementById('debugStatus');const debugState={eventCount:0};let baseMessages=[];let liveAssistant='';let source;function render(messages){baseMessages=Array.isArray(messages)?messages:[];renderCurrent()}function renderCurrent(){const visible=(baseMessages||[]).map(formatVisibleMessage).filter(Boolean);if(liveAssistant.trim()){visible.push('assistant: '+liveAssistant.trim())}log.textContent=visible.join('\n\n')}function formatVisibleMessage(message){if(!message||typeof message!=='object'){return ''}const role=typeof message.role==='string'?message.role.trim().toLowerCase():'';const type=typeof message.type==='string'?message.type.trim().toLowerCase():'';const text=typeof message.text==='string'?message.text.trim():'';if(role==='user'){return text?'user: '+text:''}if(role==='assistant'&&(!type||type==='assistant_final')){return text?'assistant: '+text:''}if(role==='assistant'&&type==='tool_call'){const toolName=typeof message.toolName==='string'?message.toolName.trim():'tool';const args=message.args&&typeof message.args==='object'?JSON.stringify(message.args,null,2):'';return 'assistant: [tool] '+toolName+(args?'\n'+args:'')}if(role==='tool'||type==='tool_result'){const toolName=typeof message.toolName==='string'?message.toolName.trim():'tool';if(!text&&!toolName){return ''}return 'tool: '+toolName+(text?'\n'+text:'')}return ''}function appendDebugLine(line){debugLog.textContent+=(debugLog.textContent?'\n':'')+line;debugLog.scrollTop=debugLog.scrollHeight}function formatDebugEvent(evt){const payload=evt&&evt.agentEvent?evt.agentEvent:{};const data=payload.data||{};const stream=payload.stream||'debug';const type=data.type||'event';if(stream==='assistant'&&type==='text_delta'){return '[token] '+(data.text||'')}if(stream==='assistant'&&type==='reasoning_delta'){return '[reasoning] '+(data.text||'')}if(stream==='tool'){return '[tool] '+type+' '+JSON.stringify(data)}return '['+stream+'] '+type+' '+JSON.stringify(data)}async function refresh(){const sessionKey=document.getElementById('sessionKey').value;const res=await fetch('/rpc/chat.history?sessionKey='+encodeURIComponent(sessionKey)+'&limit=200');if(!res.ok){return}const data=await res.json();liveAssistant='';render(data.messages||[])}function connect(){if(source){source.close();source=undefined}const sessionKey=document.getElementById('sessionKey').value;debugStatus.textContent='connected';source=new EventSource('/rpc/chat.events?sessionKey='+encodeURIComponent(sessionKey));source.addEventListener('message',async(evt)=>{try{const data=JSON.parse(evt.data);const record=data&&data.record?data.record:null;if(record&&record.kind==='block'&&record.payload&&typeof record.payload.text==='string'){liveAssistant+=record.payload.text;renderCurrent();return}if(record&&record.kind==='final'){await refresh();return}}catch{}await refresh()});['thinking','thinking_complete','streaming','message_complete','tool_call','delivery','lifecycle','debug'].forEach(function(evtType){source.addEventListener(evtType,(evt)=>{debugState.eventCount+=1;try{const data=JSON.parse(evt.data);appendDebugLine('['+evtType+'] '+formatDebugEvent(data));console.debug('chat.events '+evtType,data)}catch{appendDebugLine('['+evtType+'] '+evt.data);console.debug('chat.events '+evtType,evt.data)}debugStatus.textContent='events: '+debugState.eventCount})});source.onerror=()=>{debugStatus.textContent='stream disconnected'}}document.getElementById('send').onclick=async()=>{const sessionKey=document.getElementById('sessionKey').value;const message=document.getElementById('message').value;const res=await fetch('/rpc/chat.send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({sessionKey,message,channel:'webchat',to:'webchat-user'})});if(res.ok){document.getElementById('message').value='';await refresh()}};document.getElementById('clearDebug').onclick=()=>{debugLog.textContent='';debugState.eventCount=0;debugStatus.textContent='cleared'};document.getElementById('sessionKey').addEventListener('change',async()=>{await refresh();connect()});refresh().then(connect)
</script></body></html>`
	t := template.Must(template.New("webchat").Parse(page))
	_ = t.Execute(c.Writer, nil)
}
