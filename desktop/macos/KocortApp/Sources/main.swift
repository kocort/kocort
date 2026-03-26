import Cocoa

// Explicit entry point — required for SwiftPM without a XIB/Storyboard.
// The @main attribute does NOT bootstrap NSApplication properly in SwiftPM.
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
