// aituner.app: the native macOS shell around the aituner server (Contents/MacOS/aituner-server).
//
// It runs the server as a child in --app mode and talks to it over a line protocol: the server prints
// "link <url>" (a single-use launch link) at start and again for every line written to its stdin, and it shuts down
// when its stdin closes, so it can never outlive the app, even if the app is force-quit.
//
// The UI is the same web UI, in a native window, plus a menu bar item whose panel shows the live model and GPU.
// Closing the window keeps aituner running in the menu bar (a model being served keeps serving); Quit stops everything.
import AppKit
import WebKit

// MARK: - Server process

final class Server {
    private let proc = Process()
    private let stdin = Pipe()
    private let stdout = Pipe()
    private let stderr = Pipe()
    private var outBuf = Data()
    private var errTail = Data()
    private var waiting: [(URL) -> Void] = [] // callers waiting for a link, in request order (main thread only)
    private(set) var port = 0
    var onExit: ((Int32, String) -> Void)?
    /// Any other line from the server: ("update", "0.2.0"), ("uptodate", "0.1.0"), ("update-error", msg), ("quit", "").
    var onEvent: ((String, String) -> Void)?
    var stopping = false

    init(executable: URL) {
        proc.executableURL = executable
        proc.arguments = ["--app"]
        var env = ProcessInfo.processInfo.environment
        // Finder starts apps with a minimal PATH; aituner resolves its tools by absolute path, but anything it starts
        // inherits this, so Homebrew's directories are appended (never prepended over what the user has).
        env["PATH"] = (env["PATH"] ?? "/usr/bin:/bin:/usr/sbin:/sbin") + ":/opt/homebrew/bin:/usr/local/bin"
        proc.environment = env
        proc.standardInput = stdin
        proc.standardOutput = stdout
        proc.standardError = stderr
    }

    /// Starts the server; the first link goes to `first`.
    func start(first: @escaping (URL) -> Void) throws {
        waiting.append(first)
        stdout.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            DispatchQueue.main.async { self?.consume(d) }
        }
        stderr.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            DispatchQueue.main.async {
                guard let self else { return }
                self.errTail.append(d)
                if self.errTail.count > 4096 { self.errTail = self.errTail.suffix(4096) }
            }
        }
        proc.terminationHandler = { [weak self] p in
            DispatchQueue.main.async {
                guard let self else { return }
                self.onExit?(p.terminationStatus, String(decoding: self.errTail, as: UTF8.self))
            }
        }
        try proc.run()
    }

    /// Asks the server for a fresh single-use link.
    func link(_ then: @escaping (URL) -> Void) {
        guard proc.isRunning else { return }
        waiting.append(then)
        try? stdin.fileHandleForWriting.write(contentsOf: Data("\n".utf8))
    }

    /// Sends a command line to the server ("check", "update", "skip 0.2.0").
    func send(_ command: String) {
        guard proc.isRunning, !command.contains("\n") else { return }
        try? stdin.fileHandleForWriting.write(contentsOf: Data((command + "\n").utf8))
    }

    private func consume(_ d: Data) {
        outBuf.append(d)
        while let nl = outBuf.firstIndex(of: 0x0A) {
            let line = String(decoding: outBuf[outBuf.startIndex..<nl], as: UTF8.self)
            outBuf.removeSubrange(outBuf.startIndex...nl)
            if line.hasPrefix("link ") {
                guard let url = URL(string: String(line.dropFirst(5))), url.scheme == "http", url.host == "127.0.0.1",
                      let p = url.port else { continue }
                port = p
                if !waiting.isEmpty { waiting.removeFirst()(url) }
            } else {
                let parts = line.split(separator: " ", maxSplits: 1).map(String.init)
                if let verb = parts.first { onEvent?(verb, parts.count > 1 ? parts[1] : "") }
            }
        }
    }

    /// Stops the server: closing stdin asks it to shut down cleanly (it stops the model server too); SIGTERM, then
    /// SIGKILL, only if it does not finish in time.
    func stop(done: @escaping () -> Void) {
        stopping = true
        guard proc.isRunning else { done(); return }
        try? stdin.fileHandleForWriting.close()
        DispatchQueue.global().async { [proc] in
            func wait(_ s: Double) -> Bool {
                let end = Date().addingTimeInterval(s)
                while proc.isRunning && Date() < end { Thread.sleep(forTimeInterval: 0.1) }
                return !proc.isRunning
            }
            if !wait(15) { proc.terminate(); if !wait(5) { kill(proc.processIdentifier, SIGKILL) } }
            DispatchQueue.main.async(execute: done)
        }
    }
}

// MARK: - Web views

/// Shared by the window and the menu bar panel: only the server's own origin loads inside; http(s) links open in the
/// default browser; downloads (report exports) go through a save panel.
final class WebPolicy: NSObject, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    weak var server: Server?

    func isServer(_ url: URL?) -> Bool {
        guard let url, let server else { return false }
        return url.scheme == "http" && url.host == "127.0.0.1" && url.port == server.port
    }

    private func openExternally(_ url: URL?) {
        if let url, url.scheme == "https" || url.scheme == "http" { NSWorkspace.shared.open(url) }
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
        if action.shouldPerformDownload { return .download }
        if isServer(action.request.url) || action.request.url?.scheme == "about" { return .allow }
        openExternally(action.request.url)
        return .cancel
    }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse) async -> WKNavigationResponsePolicy {
        if let r = response.response as? HTTPURLResponse,
           (r.value(forHTTPHeaderField: "Content-Disposition") ?? "").lowercased().hasPrefix("attachment") {
            return .download
        }
        return response.canShowMIMEType ? .allow : .download
    }

    // target=_blank (e.g. a model's page on Hugging Face)
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        openExternally(action.request.url)
        return nil
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String) async -> URL? {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
        return panel.runModal() == .OK ? panel.url : nil
    }
}

func makeWebView(policy: WebPolicy, handler: WKScriptMessageHandler? = nil) -> WKWebView {
    let config = WKWebViewConfiguration()
    config.websiteDataStore = .default() // one cookie jar: the window and the panel share the session
    if let handler { config.userContentController.add(handler, name: "aituner") }
    let wv = WKWebView(frame: .zero, configuration: config)
    wv.navigationDelegate = policy
    wv.uiDelegate = policy
    wv.allowsBackForwardNavigationGestures = false
    return wv
}

// MARK: - App

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, NSPopoverDelegate, WKScriptMessageHandler {
    private var server: Server!
    private let policy = WebPolicy()
    private var window: NSWindow?
    private var webView: WKWebView?
    private var statusItem: NSStatusItem!
    private let popover = NSPopover()
    private var panelView: WKWebView!
    private let panelWidth: CGFloat = 340

    func applicationDidFinishLaunching(_ note: Notification) {
        buildMenu()
        buildStatusItem()
        guard let exe = Bundle.main.url(forAuxiliaryExecutable: "aituner-server") else {
            fail("This copy of aituner is incomplete (aituner-server is missing). Rebuild it with build_app.sh.")
            return
        }
        server = Server(executable: exe)
        policy.server = server
        server.onExit = { [weak self] status, stderr in
            guard let self, !self.server.stopping else { return }
            let detail = stderr.split(separator: "\n").last.map(String.init) ?? "exit status \(status)"
            self.fail("aituner stopped unexpectedly.\n\n\(detail)")
        }
        server.onEvent = { [weak self] verb, arg in self?.handle(verb, arg) }
        do {
            try server.start { [weak self] url in self?.showWindow(loading: url) }
        } catch {
            fail("aituner could not start: \(error.localizedDescription)")
        }
    }

    // Closing the window leaves aituner in the menu bar; the Dock icon (or the menu bar) brings the window back.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        showWindow()
        return false
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let server else { return .terminateNow }
        server.stop { NSApp.reply(toApplicationShouldTerminate: true) }
        return .terminateLater
    }

    private func fail(_ message: String) {
        server?.stopping = true
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        let alert = NSAlert()
        alert.messageText = "aituner"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
        NSApp.terminate(nil)
    }

    // MARK: software update

    private var manualCheck = false
    private var installing = false

    @objc func checkForUpdates(_ sender: Any?) {
        manualCheck = true
        server?.send("check")
    }

    private func handle(_ verb: String, _ arg: String) {
        switch verb {
        case "update":
            manualCheck = false
            promptUpdate(version: arg)
        case "uptodate":
            if manualCheck { inform("aituner is up to date", "You have the latest version (\(arg)).") }
            manualCheck = false
        case "update-error":
            if manualCheck || installing { inform("The update did not complete", arg) }
            manualCheck = false
            installing = false
        case "quit":
            // an update is verified and staged: quit so the helper can swap it in and relaunch
            server?.stopping = true
            NSApp.terminate(nil)
        default:
            break
        }
    }

    private func promptUpdate(version: String) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        let current = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let alert = NSAlert()
        alert.messageText = "aituner \(version) is available"
        alert.informativeText = "You have \(current). The update is downloaded from GitHub, checked (Developer ID signature and Apple notarization) and installed, then aituner restarts. A running model is stopped."
        alert.addButton(withTitle: "Update Now")
        alert.addButton(withTitle: "Later")
        alert.addButton(withTitle: "Skip This Version")
        switch alert.runModal() {
        case .alertFirstButtonReturn:
            installing = true
            server?.send("update")
        case .alertThirdButtonReturn:
            server?.send("skip \(version)")
        default:
            break
        }
        if window?.isVisible != true { NSApp.setActivationPolicy(.accessory) }
    }

    private func inform(_ title: String, _ text: String) {
        NSApp.activate()
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = text
        alert.runModal()
    }

    // MARK: window

    @objc func openWindow(_ sender: Any?) { showWindow() }

    private func showWindow(loading url: URL? = nil) {
        if window == nil {
            let wv = makeWebView(policy: policy)
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1120, height: 820),
                             styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            w.title = "aituner"
            w.contentView = wv
            w.contentMinSize = NSSize(width: 380, height: 520)
            w.isReleasedWhenClosed = false
            w.delegate = self
            w.center()
            w.setFrameAutosaveName("aituner.main")
            window = w
            webView = wv
            if url == nil { server.link { [weak wv] u in wv?.load(URLRequest(url: u)) } }
        }
        if let url { webView?.load(URLRequest(url: url)) }
        NSApp.setActivationPolicy(.regular)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    func windowWillClose(_ note: Notification) {
        // no window: live in the menu bar only (a Dock icon the user pinned stays, and reopens the window)
        NSApp.setActivationPolicy(.accessory)
    }

    @objc func reload(_ sender: Any?) { webView?.reload() }

    @objc func openInBrowser(_ sender: Any?) {
        server.link { NSWorkspace.shared.open($0) }
    }

    // MARK: menu bar item and panel

    private func buildStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = statusItem.button {
            let img = NSImage(systemSymbolName: "gauge.with.needle", accessibilityDescription: "aituner")
                ?? NSImage(systemSymbolName: "gauge", accessibilityDescription: "aituner")
            img?.isTemplate = true
            button.image = img
            button.action = #selector(togglePanel(_:))
            button.target = self
        }
        panelView = makeWebView(policy: policy, handler: self)
        let vc = NSViewController()
        vc.view = panelView
        popover.contentViewController = vc
        popover.contentSize = NSSize(width: panelWidth, height: 300)
        popover.behavior = .transient
        popover.animates = true
        popover.delegate = self
    }

    @objc func togglePanel(_ sender: Any?) {
        if popover.isShown { popover.performClose(sender); return }
        guard let button = statusItem.button, let server else { return }
        // a fresh page each time it opens; it polls only while shown
        server.link { [weak self] url in
            guard var c = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return }
            let items = c.queryItems ?? []
            c.queryItems = items + [URLQueryItem(name: "view", value: "menubar")]
            if let u = c.url { self?.panelView.load(URLRequest(url: u)) }
        }
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        popover.contentViewController?.view.window?.makeKey()
    }

    func popoverDidClose(_ note: Notification) {
        panelView.load(URLRequest(url: URL(string: "about:blank")!))
    }

    // Messages from the panel page: "open", "quit", or {height: n}. Only the server's own page may send them.
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, policy.isServer(message.frameInfo.request.url) else { return }
        if let cmd = message.body as? String {
            switch cmd {
            case "open": popover.performClose(nil); showWindow()
            case "quit": NSApp.terminate(nil)
            default: break
            }
        } else if let d = message.body as? [String: Any], let h = d["height"] as? Double {
            popover.contentSize = NSSize(width: panelWidth, height: min(max(h, 120), 640))
        }
    }

    // MARK: main menu

    private func buildMenu() {
        let main = NSMenu()
        func sub(_ title: String, _ items: [NSMenuItem]) {
            let m = NSMenu(title: title)
            items.forEach(m.addItem)
            let it = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            it.submenu = m
            main.addItem(it)
        }
        func item(_ t: String, _ a: Selector?, _ k: String = "", _ mods: NSEvent.ModifierFlags = .command) -> NSMenuItem {
            let i = NSMenuItem(title: t, action: a, keyEquivalent: k)
            i.keyEquivalentModifierMask = mods
            return i
        }
        sub("aituner", [
            item("About aituner", #selector(NSApplication.orderFrontStandardAboutPanel(_:))),
            {
                let i = item("Check for Updates…", #selector(checkForUpdates(_:)))
                i.target = self
                return i
            }(),
            .separator(),
            item("Hide aituner", #selector(NSApplication.hide(_:)), "h"),
            item("Hide Others", #selector(NSApplication.hideOtherApplications(_:)), "h", [.command, .option]),
            item("Show All", #selector(NSApplication.unhideAllApplications(_:))),
            .separator(),
            item("Quit aituner", #selector(NSApplication.terminate(_:)), "q"),
        ])
        sub("Edit", [
            item("Undo", Selector(("undo:")), "z"),
            item("Redo", Selector(("redo:")), "z", [.command, .shift]),
            .separator(),
            item("Cut", #selector(NSText.cut(_:)), "x"),
            item("Copy", #selector(NSText.copy(_:)), "c"),
            item("Paste", #selector(NSText.paste(_:)), "v"),
            item("Select All", #selector(NSText.selectAll(_:)), "a"),
        ])
        let open = item("Open aituner", #selector(openWindow(_:)), "0")
        let rel = item("Reload", #selector(reload(_:)), "r")
        let browser = item("Open in Browser", #selector(openInBrowser(_:)), "b", [.command, .shift])
        [open, rel, browser].forEach { $0.target = self }
        sub("View", [open, rel, browser])
        let win = NSMenu(title: "Window")
        win.addItem(item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"))
        win.addItem(item("Zoom", #selector(NSWindow.performZoom(_:))))
        win.addItem(item("Close", #selector(NSWindow.performClose(_:)), "w"))
        let winItem = NSMenuItem(title: "Window", action: nil, keyEquivalent: "")
        winItem.submenu = win
        main.addItem(winItem)
        NSApp.windowsMenu = win
        NSApp.mainMenu = main
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
