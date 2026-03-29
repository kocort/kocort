package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kocort/kocort/api"
	"github.com/kocort/kocort/internal/acpbridge"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/runtime"
)

func main() {
	var (
		configDir  = flag.String("config-dir", "", "Config root directory override (defaults to PWD/.kocort then user config dir)")
		configPath = flag.String("config", "", "Path to main kocort JSON config (defaults to <config-dir>/kocort.json)")
		modelsPath = flag.String("models-config", "", "Path to models overlay JSON config (defaults to <config-dir>/models.json)")
		chansPath  = flag.String("channels-config", "", "Path to channels overlay JSON config (defaults to <config-dir>/channels.json)")
		gatewayRun = flag.Bool("gateway", false, "Run HTTP gateway/webchat server")
		acpRun     = flag.Bool("acp", false, "Run ACP bridge over stdio")
		acpSession = flag.String("acp-session", "", "Default ACP session key")
		acpLabel   = flag.String("acp-session-label", "", "Default ACP session label")
		acpRequire = flag.Bool("acp-require-existing", false, "Require ACP session key or label to already exist")
		acpReset   = flag.Bool("acp-reset-session", false, "Reset ACP session before first use")
		acpNoCwd   = flag.Bool("acp-no-prefix-cwd", false, "Do not prefix ACP prompts with the working directory")
		acpProv    = flag.String("acp-provenance", "off", "ACP provenance mode: off, meta, or meta+receipt")
		message    = flag.String("message", "", "User message to run")
		agentID    = flag.String("agent", "main", "Agent id")
		provider   = flag.String("provider", "", "Model provider override")
		model      = flag.String("model", "", "Model id override")
		stateDir   = flag.String("state-dir", "", "State directory override")
		channel    = flag.String("channel", "cli", "Channel label")
		to         = flag.String("to", "local-user", "Peer/session target")
		timeout    = flag.Duration("timeout", 90*time.Second, "Run timeout")
	)
	flag.Parse()

	// No arguments at all → default to gateway mode
	if len(os.Args) == 1 {
		*gatewayRun = true
	}

	// Resolve config dir: CLI flag → KOCORT_CONFIG_DIR env → PWD/.kocort → user config dir
	if *configDir == "" {
		*configDir = config.ResolveDefaultConfigDir()
	}

	// Resolve to absolute path
	absConfigDir, err := filepath.Abs(*configDir)
	if err != nil {
		fail("resolve config-dir: %v", err)
	}
	*configDir = absConfigDir

	// Set KOCORT_HOME to config dir if not already set (backward compatibility)
	if os.Getenv("KOCORT_HOME") == "" {
		os.Setenv("KOCORT_HOME", absConfigDir)
	}

	if !*gatewayRun && !*acpRun && *message == "" {
		fail("missing required -message")
	}
	loadOpts := config.ConfigLoadOptions{
		ConfigDir:          *configDir,
		ConfigPath:         *configPath,
		ModelsConfigPath:   *modelsPath,
		ChannelsConfigPath: *chansPath,
	}
	cfg, err := config.LoadRuntimeConfig(config.DefaultConfigJSON(), loadOpts)
	if err != nil {
		fail("load config: %v", err)
	}
	// Resolve all relative paths in config based on the config directory.
	config.ResolveConfigPaths(&cfg, *configDir)

	// Resolve effective state directory from config, CLI flag, or default.
	effectiveStateDir := *stateDir
	if effectiveStateDir == "" {
		effectiveStateDir = config.ResolveStateDirFromConfig(cfg, *configDir)
	}

	rt, err := runtime.NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		AgentID:    *agentID,
		StateDir:   effectiveStateDir,
		Provider:   *provider,
		Model:      *model,
		Deliverer:  &delivery.StdoutDeliverer{},
		ConfigLoad: loadOpts,
	})
	if err != nil {
		fail("build runtime: %v", err)
	}
	// webhook := generic.NewGenericJSONChannelAdapter("webhook")
	// rt.Channels.RegisterIntegration(channelPkg.ChannelIntegration{
	// 	ID:           webhook.ID(),
	// 	Transport:    webhook,
	// 	Outbound:     webhook,
	// 	Capabilities: webhook,
	// })
	if *gatewayRun {
		server := api.NewServer(rt, cfg.Gateway)
		if err := server.Start(context.Background()); err != nil {
			fail("run gateway: %v", err)
		}
		return
	}
	if *acpRun {
		if err := acpbridge.ServeACPBridge(context.Background(), rt, acpbridge.ACPBridgeOptions{
			DefaultSessionKey:   *acpSession,
			DefaultSessionLabel: *acpLabel,
			RequireExisting:     *acpRequire,
			ResetSession:        *acpReset,
			PrefixCwd:           !*acpNoCwd,
			ProvenanceMode:      *acpProv,
		}, os.Stdout, os.Stdin); err != nil {
			fail("run acp bridge: %v", err)
		}
		return
	}
	ctx := context.Background()
	cancel := func() {}
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	}
	defer cancel()
	_, err = rt.Run(ctx, core.AgentRunRequest{
		AgentID: *agentID,
		Message: *message,
		Channel: *channel,
		To:      *to,
		Deliver: true,
		Timeout: *timeout,
	})
	if err != nil {
		fail("run agent: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
