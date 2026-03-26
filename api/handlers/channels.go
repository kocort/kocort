package handlers

// Channels HTTP handlers for integration management.

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	goqrcode "github.com/skip2/go-qrcode"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/channel/weixin"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/runtime"
)

// Channels holds dependencies for channels handlers.
type Channels struct {
	Runtime *runtime.Runtime
}

// List handles GET /api/integrations/channels.
func (h *Channels) List(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildChannelsState(h.Runtime))
}

// Save handles POST /api/integrations/channels/save.
func (h *Channels) Save(c *gin.Context) {
	var req types.ChannelsSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.ModifyAndPersist(h.Runtime, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		cfg.Channels = req.Channels
		return service.ConfigSections{Channels: true}, nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, service.BuildChannelsState(h.Runtime))
}

// WeixinQRStart handles POST /api/integrations/channels/weixin/qr/start.
// It requests a new QR code from the WeChat iLink API for bot login.
func (h *Channels) WeixinQRStart(c *gin.Context) {
	var req struct {
		BaseURL string `json:"baseUrl"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.BaseURL == "" {
		req.BaseURL = "https://ilinkai.weixin.qq.com"
	}

	client := weixin.NewClient(nil)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	qr, err := client.FetchQRCode(ctx, req.BaseURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// qrcode_img_content from WeChat is a web page URL, not an image.
	// We generate a QR code image from this URL so the frontend can display it.
	imgContent := ""
	qrURL := qr.QRCodeImgContent
	if qrURL != "" {
		png, err := goqrcode.Encode(qrURL, goqrcode.Medium, 256)
		if err == nil {
			imgContent = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"qrcode":           qr.QRCode,
		"qrcodeImgContent": imgContent,
	})
}

// WeixinQRPoll handles POST /api/integrations/channels/weixin/qr/poll.
// It polls the QR code login status. The upstream API long-polls for ~30s.
func (h *Channels) WeixinQRPoll(c *gin.Context) {
	var req struct {
		BaseURL string `json:"baseUrl"`
		QRCode  string `json:"qrcode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.QRCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "qrcode is required"})
		return
	}
	if req.BaseURL == "" {
		req.BaseURL = "https://ilinkai.weixin.qq.com"
	}

	client := weixin.NewClient(nil)
	status, err := client.PollQRStatus(c.Request.Context(), req.BaseURL, req.QRCode)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   status.Status,
		"botToken": status.BotToken,
		"botId":    status.ILinkBotID,
		"baseUrl":  status.BaseURL,
	})
}
