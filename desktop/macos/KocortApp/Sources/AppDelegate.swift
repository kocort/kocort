import Cocoa

class AppDelegate: NSObject, NSApplicationDelegate {

    // MARK: - Localization

    /// Simple inline localization: picks Chinese when the system preferred
    /// language starts with "zh", English otherwise.
    private enum L10n {
        static let isZH: Bool = {
            let lang = Locale.preferredLanguages.first ?? "en"
            return lang.hasPrefix("zh")
        }()

        static func t(_ en: String, _ zh: String) -> String {
            isZH ? zh : en
        }

        // Status line (disabled menu item)
        static var statusStarting: String {
            t("◑ Service Starting (Port %d)", "◑ 服务启动中 (端口 %d)")
        }
        static var statusRunning: String {
            t("● Service Running (Port %d)", "● 服务运行中 (端口 %d)")
        }
        static var statusStopped: String {
            t("○ Service Stopped", "○ 服务已停止")
        }

        // Menu items
        static var openDashboard: String { t("Open Dashboard", "打开管理端") }
        static var restartServer: String { t("Restart Server", "重启服务") }
        static var viewLogs: String { t("View Logs", "查看日志") }
        static var about: String { t("About Kocort", "关于 Kocort") }
        static var quit: String { t("Quit", "退出") }
        static var ok: String { t("OK", "确定") }

        // Alerts
        static var startFailed: String { t("Start Failed", "启动失败") }
        static var binaryNotFound: String {
            t(
                "Cannot find the kocort binary.\n\nSearched:\n1. Bundle Resources\n2. Directory next to Bundle\n3. System PATH",
                "找不到 kocort 二进制文件。\n\n已搜索:\n1. Bundle Resources\n2. Bundle 同级目录\n3. 系统 PATH")
        }
        static var startError: String {
            t("Failed to start kocort service:", "无法启动 kocort 服务:")
        }
        static var logTitle: String { t("Logs", "日志") }
        static var noLogFile: String { t("No log file yet.", "还没有日志文件。") }
        static var notFound: String { t("Not Found", "未找到") }

        // About dialog labels
        static var aboutServiceAddr: String { t("Service Address", "服务地址") }
        static var aboutConfigDir: String { t("Config Directory", "配置目录") }
        static var aboutGoBinary: String { t("Go Binary", "Go 二进制") }
    }

    // MARK: - Properties
    private var statusItem: NSStatusItem!
    private var serverProcess: Process?
    private var serverPort: Int = 18789
    private var isServerRunning = false
    private var isServerStarting = false
    private var startupProbeID = UUID()

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Run as a menubar-only app (no Dock icon)
        NSApp.setActivationPolicy(.accessory)

        setupStatusItem()
        startGoServer(autoOpenDashboard: true)
    }

    func applicationWillTerminate(_ notification: Notification) {
        stopGoServer()
    }

    func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
        return true
    }

    // MARK: - Status Bar Setup

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)

        if let button = statusItem.button {
            button.image = loadStatusBarImage()
            // Final text fallback
            if button.image == nil {
                button.title = "K"
            }
            button.toolTip = "Kocort AI Agent"
        }

        updateMenu()
    }

    private func loadStatusBarImage() -> NSImage? {
        let bundle = Bundle.main

        // 1. Prefer "trayTemplate" — macOS recognises the "Template" suffix and
        //    automatically renders the icon in the correct colour for light/dark
        //    menu bar.  The image must be a monochrome shape on a transparent bg.
        let templateCandidates = ["trayTemplate", "tray"]
        for name in templateCandidates {
            if let image = bundle.image(forResource: name) {
                image.size = NSSize(width: 18, height: 18)
                image.isTemplate = true
                return image
            }
            for ext in ["png", "pdf"] {
                if let path = bundle.path(forResource: name, ofType: ext),
                    let image = NSImage(contentsOfFile: path)
                {
                    image.size = NSSize(width: 18, height: 18)
                    image.isTemplate = true
                    return image
                }
            }
        }

        // 2. Fall back to app icon (not template)
        for name in ["AppIcon", "icon"] {
            if let image = bundle.image(forResource: name)
                ?? bundle.image(forResource: NSImage.Name(name))
            {
                image.size = NSSize(width: 18, height: 18)
                return image
            }
            for ext in ["png", "icns"] {
                if let path = bundle.path(forResource: name, ofType: ext),
                    let image = NSImage(contentsOfFile: path)
                {
                    image.size = NSSize(width: 18, height: 18)
                    return image
                }
            }
        }

        // 3. SF Symbols fallback
        if #available(macOS 11.0, *) {
            let sfCandidates = ["brain.head.profile", "server.rack"]
            for symbol in sfCandidates {
                if let image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Kocort")
                {
                    image.size = NSSize(width: 18, height: 18)
                    return image
                }
            }
        }

        return nil
    }

    private func updateMenu() {
        let menu = NSMenu()
        menu.autoenablesItems = false

        // Status indicator (uses Unicode geometric dots, not emoji)
        // Use NSAttributedString so the colour is always visible even though
        // the item is disabled (disabled items are normally rendered grey).
        let statusTitle: String
        let statusColor: NSColor
        if isServerStarting {
            statusTitle = String(format: L10n.statusStarting, serverPort)
            statusColor = .systemOrange
        } else if isServerRunning {
            statusTitle = String(format: L10n.statusRunning, serverPort)
            statusColor = .systemGreen
        } else {
            statusTitle = L10n.statusStopped
            statusColor = .secondaryLabelColor
        }
        let statusMenuItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        statusMenuItem.attributedTitle = NSAttributedString(
            string: statusTitle,
            attributes: [
                .foregroundColor: statusColor,
                .font: NSFont.menuFont(ofSize: 0),
            ])
        statusMenuItem.isEnabled = false
        menu.addItem(statusMenuItem)

        menu.addItem(NSMenuItem.separator())

        // Open Dashboard
        let openItem = NSMenuItem(
            title: L10n.openDashboard,
            action: #selector(openDashboard),
            keyEquivalent: "o")
        openItem.target = self
        openItem.isEnabled = isServerRunning && !isServerStarting
        menu.addItem(openItem)

        menu.addItem(NSMenuItem.separator())

        // Restart
        let restartItem = NSMenuItem(
            title: L10n.restartServer,
            action: #selector(restartServer),
            keyEquivalent: "r")
        restartItem.target = self
        menu.addItem(restartItem)

        // View Logs
        let logsItem = NSMenuItem(
            title: L10n.viewLogs,
            action: #selector(viewLogs),
            keyEquivalent: "l")
        logsItem.target = self
        menu.addItem(logsItem)

        menu.addItem(NSMenuItem.separator())

        // About
        let aboutItem = NSMenuItem(
            title: L10n.about,
            action: #selector(showAbout),
            keyEquivalent: "")
        aboutItem.target = self
        menu.addItem(aboutItem)

        menu.addItem(NSMenuItem.separator())

        // Quit
        let quitItem = NSMenuItem(
            title: L10n.quit,
            action: #selector(quitApp),
            keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)

        self.statusItem.menu = menu
    }

    // MARK: - Go Server Management

    private func startGoServer(autoOpenDashboard: Bool) {
        guard let binaryPath = serverBinaryPath() else {
            NSLog("ERROR: Cannot find kocort binary in bundle or PATH")
            showAlert(
                title: L10n.startFailed,
                message: L10n.binaryNotFound)
            return
        }

        NSLog("Found kocort binary at: %@", binaryPath)

        startupProbeID = UUID()
        isServerStarting = true
        isServerRunning = false
        updateMenu()

        let configDir = dotKocortDir()
        ensureDirectory(at: configDir)
        seedDefaultConfigIfNeeded(configDir: configDir)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: binaryPath)
        process.arguments = ["--gateway", "--config-dir", configDir]

        // Set up environment
        var env = ProcessInfo.processInfo.environment
        env["KOCORT_HOME"] = configDir
        process.environment = env

        // Redirect output to log file
        let logFile = configDir + "/kocort-server.log"
        FileManager.default.createFile(atPath: logFile, contents: nil)
        if let fileHandle = FileHandle(forWritingAtPath: logFile) {
            fileHandle.seekToEndOfFile()
            process.standardOutput = fileHandle
            process.standardError = fileHandle
        }

        // Handle unexpected termination
        process.terminationHandler = { [weak self] proc in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if self.isServerRunning || self.isServerStarting {
                    self.isServerStarting = false
                    self.isServerRunning = false
                    self.updateMenu()
                    NSLog(
                        "Kocort server terminated unexpectedly with code %d", proc.terminationStatus
                    )
                }
            }
        }

        do {
            try process.run()
            serverProcess = process
            scheduleServerReadinessCheck(autoOpenDashboard: autoOpenDashboard)
            NSLog(
                "Kocort server started (PID: %d, port: %d)", process.processIdentifier, serverPort)
        } catch {
            isServerStarting = false
            NSLog("ERROR starting kocort: %@", error.localizedDescription)
            updateMenu()
            showAlert(
                title: L10n.startFailed,
                message: "\(L10n.startError)\n\(error.localizedDescription)")
        }
    }

    private func stopGoServer() {
        startupProbeID = UUID()
        guard let process = serverProcess, process.isRunning else { return }
        isServerStarting = false
        isServerRunning = false
        updateMenu()
        process.terminate()
        DispatchQueue.global().async {
            process.waitUntilExit()
        }
        serverProcess = nil
        NSLog("Kocort server stopped")
    }

    // MARK: - Actions

    @objc private func openDashboard() {
        let url = URL(string: "http://127.0.0.1:\(serverPort)")!
        NSWorkspace.shared.open(url)
    }

    @objc private func restartServer() {
        NSLog("Restarting kocort server...")
        stopGoServer()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            self?.startGoServer(autoOpenDashboard: false)
        }
    }

    @objc private func viewLogs() {
        let logFile = dotKocortDir() + "/kocort-server.log"
        if FileManager.default.fileExists(atPath: logFile) {
            NSWorkspace.shared.open(URL(fileURLWithPath: logFile))
        } else {
            showAlert(title: L10n.logTitle, message: L10n.noLogFile)
        }
    }

    @objc private func showAbout() {
        // Bring app to front for the alert
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "Kocort"
        alert.informativeText = """
            AI Agent

            \(L10n.aboutServiceAddr): http://127.0.0.1:\(serverPort)
            \(L10n.aboutConfigDir): \(dotKocortDir())
            \(L10n.aboutGoBinary): \(serverBinaryPath() ?? L10n.notFound)
            """
        alert.alertStyle = .informational
        alert.addButton(withTitle: L10n.ok)
        alert.runModal()
    }

    @objc private func quitApp() {
        stopGoServer()
        NSApp.terminate(nil)
    }

    // MARK: - Helpers

    /// Returns the path to the kocort Go binary.
    private func serverBinaryPath() -> String? {
        // 1. Bundle Resources (production)
        if let bundledPath = Bundle.main.path(forResource: "kocort", ofType: nil) {
            if FileManager.default.isExecutableFile(atPath: bundledPath) {
                return bundledPath
            }
        }
        // 2. Next to .app bundle (development)
        let appDir = Bundle.main.bundlePath
        let devPath = (appDir as NSString).deletingLastPathComponent + "/kocort"
        if FileManager.default.isExecutableFile(atPath: devPath) {
            return devPath
        }
        // 3. Same directory structure paths
        let sameDirPaths = [
            (appDir as NSString).deletingLastPathComponent + "/macos_arm64/kocort",
            (appDir as NSString).deletingLastPathComponent + "/macos_amd64/kocort",
            (appDir as NSString).deletingLastPathComponent + "/macos_universal/kocort",
        ]
        for p in sameDirPaths {
            if FileManager.default.isExecutableFile(atPath: p) {
                return p
            }
        }
        // 4. Search PATH
        let whichProcess = Process()
        whichProcess.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        whichProcess.arguments = ["kocort"]
        let pipe = Pipe()
        whichProcess.standardOutput = pipe
        whichProcess.standardError = FileHandle.nullDevice
        try? whichProcess.run()
        whichProcess.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        if let path = String(data: data, encoding: .utf8)?.trimmingCharacters(
            in: .whitespacesAndNewlines),
            !path.isEmpty
        {
            return path
        }
        return nil
    }

    /// Returns the config directory for the desktop app.
    ///
    /// - **Non-sandboxed** (direct distribution): `~/.kocort`
    /// - **Sandboxed** (App Store): `~/Library/Containers/<bundle>/Data/.kocort`
    ///
    /// In sandbox mode the home directory is already remapped to the container,
    /// so `~/.kocort` transparently lands inside the container.  We detect
    /// sandbox by checking the `APP_SANDBOX_CONTAINER_ID` environment variable
    /// that macOS injects into sandboxed processes.
    private var isSandboxed: Bool {
        ProcessInfo.processInfo.environment["APP_SANDBOX_CONTAINER_ID"] != nil
    }

    private func dotKocortDir() -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser
        if isSandboxed {
            // In sandbox, ~ is already the container.  Use Application Support
            // for a cleaner layout that survives container resets.
            let appSupport = home.appendingPathComponent("Library/Application Support/Kocort")
            return appSupport.path
        }
        return home.appendingPathComponent(".kocort").path
    }

    private func ensureDirectory(at path: String) {
        try? FileManager.default.createDirectory(
            atPath: path,
            withIntermediateDirectories: true,
            attributes: nil)
    }

    private func scheduleServerReadinessCheck(autoOpenDashboard: Bool) {
        let probeID = startupProbeID
        let deadline = Date().addingTimeInterval(20)

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            self?.pollServerReadiness(
                probeID: probeID,
                deadline: deadline,
                autoOpenDashboard: autoOpenDashboard)
        }
    }

    private func pollServerReadiness(probeID: UUID, deadline: Date, autoOpenDashboard: Bool) {
        guard probeID == startupProbeID else { return }
        guard let process = serverProcess, process.isRunning else { return }

        if isDashboardReachable() {
            DispatchQueue.main.async { [weak self] in
                guard let self = self, probeID == self.startupProbeID else { return }
                self.isServerStarting = false
                self.isServerRunning = true
                self.updateMenu()
                if autoOpenDashboard {
                    self.openDashboard()
                }
            }
            return
        }

        if Date() >= deadline {
            DispatchQueue.main.async { [weak self] in
                guard let self = self, probeID == self.startupProbeID else { return }
                self.isServerStarting = false
                self.isServerRunning = false
                self.updateMenu()
                NSLog("WARN: Kocort server readiness check timed out")
            }
            return
        }

        Thread.sleep(forTimeInterval: 0.25)
        pollServerReadiness(
            probeID: probeID, deadline: deadline, autoOpenDashboard: autoOpenDashboard)
    }

    private func isDashboardReachable() -> Bool {
        guard let url = URL(string: "http://127.0.0.1:\(serverPort)/") else {
            return false
        }

        var request = URLRequest(url: url)
        request.timeoutInterval = 0.8
        request.cachePolicy = .reloadIgnoringLocalCacheData

        let semaphore = DispatchSemaphore(value: 0)
        var reachable = false

        let task = URLSession.shared.dataTask(with: request) { _, response, error in
            if error == nil, response is HTTPURLResponse {
                reachable = true
            }
            semaphore.signal()
        }
        task.resume()

        _ = semaphore.wait(timeout: .now() + 1.0)
        return reachable
    }

    private func showAlert(title: String, message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.addButton(withTitle: L10n.ok)
        alert.runModal()
    }

    /// Seeds minimal config files into configDir so kocort can start the
    /// gateway even when no real provider is configured yet.
    private func seedDefaultConfigIfNeeded(configDir: String) {
        let fm = FileManager.default

        let kocortJson = configDir + "/kocort.json"
        if !fm.fileExists(atPath: kocortJson) {
            // Check if the bundle carries a default config
            if let bundled = Bundle.main.path(forResource: "kocort", ofType: "json") {
                try? fm.copyItem(atPath: bundled, toPath: kocortJson)
                NSLog("Seeded kocort.json from bundle")
            } else {
                // Write a minimal config that lets the gateway start
                let minimal = """
                    {
                      "agents": {
                        "defaults": {
                          "model": { "primary": "openai/gpt-4o-mini" },
                          "timeoutSeconds": 180
                        },
                        "catalog": [
                          {
                            "id": "main",
                            "name": "Main",
                            "model": { "primary": "openai/gpt-4o-mini" },
                            "systemPrompt": "You are a helpful assistant."
                          }
                        ]
                      },
                      "gateway": {
                        "enabled": true,
                        "bind": "loopback",
                        "port": 18789
                      }
                    }
                    """
                fm.createFile(atPath: kocortJson, contents: minimal.data(using: .utf8))
                NSLog("Seeded minimal kocort.json")
            }
        }

        let modelsJson = configDir + "/models.json"
        if !fm.fileExists(atPath: modelsJson) {
            if let bundled = Bundle.main.path(forResource: "models", ofType: "json") {
                try? fm.copyItem(atPath: bundled, toPath: modelsJson)
                NSLog("Seeded models.json from bundle")
            } else {
                let minimal = """
                    {
                      "providers": {
                        "openai": {
                          "kind": "openai",
                          "apiKey": "${OPENAI_API_KEY}"
                        }
                      }
                    }
                    """
                fm.createFile(atPath: modelsJson, contents: minimal.data(using: .utf8))
                NSLog("Seeded minimal models.json")
            }
        }

        let channelsJson = configDir + "/channels.json"
        if !fm.fileExists(atPath: channelsJson) {
            if let bundled = Bundle.main.path(forResource: "channels", ofType: "json") {
                try? fm.copyItem(atPath: bundled, toPath: channelsJson)
            } else {
                let minimal = """
                    { "channels": [] }
                    """
                fm.createFile(atPath: channelsJson, contents: minimal.data(using: .utf8))
            }
            NSLog("Seeded channels.json")
        }
    }
}
