import Cocoa
import WebKit

/// 应用委托与外壳行为（施工单 035）：窗口、托盘、菜单、导航、下载、退出。
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate,
    WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate
{
    private let instanceLock: InstanceLock
    private let defaults = UserDefaults.standard
    private let frameKey = "GQYWindowFrame"
    private let desktopLogName = "desktop.log"

    private var window: NSWindow!
    private var webView: WKWebView!
    private var server: GQYServer?
    private var serverPort: UInt16 = 0
    private var statusItem: NSStatusItem?
    private var statusMenu: NSMenu?
    private var showObserver: NSObjectProtocol?
    private var powerObserver: NSObjectProtocol?
    private var activeDownloads: [WKDownload] = []
    private var desktopLog: FileHandle?
    private var isQuitting = false
    private var isShuttingDown = false
    private var isApplyingFrame = false
    private var isShowingAlert = false

    init(instanceLock: InstanceLock) {
        self.instanceLock = instanceLock
        super.init()
    }

    // MARK: - 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        openDesktopLog()
        log("launch")
        observeShellEvents()
        buildMenu()
        buildTray()
        buildWindow()
        startServer()
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationWillTerminate(_ notification: Notification) {
        if !isShuttingDown {
            server?.stop(timeout: 2)
        }
        instanceLock.release()
        log("terminated")
        try? desktopLog?.close()
        desktopLog = nil
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showWindow(nil)
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if isShuttingDown {
            return .terminateNow
        }
        if isQuitting {
            return .terminateCancel
        }
        isQuitting = true
        log("quit requested")
        window?.orderOut(nil)
        removeTray()
        let current = server
        server = nil
        if current == nil {
            return .terminateNow
        }
        DispatchQueue.global(qos: .userInitiated).async {
            current?.stop(timeout: 10)
            DispatchQueue.main.async {
                NSApp.reply(toApplicationShouldTerminate: true)
            }
        }
        return .terminateLater
    }

    // MARK: - 外壳事件

    private func observeShellEvents() {
        showObserver = DistributedNotificationCenter.default().addObserver(
            forName: InstanceLock.showNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            self?.showWindow(nil)
        }
        powerObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.willPowerOffNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            self?.isShuttingDown = true
            self?.log("system is shutting down")
        }
    }

    // MARK: - 菜单

    private func buildMenu() {
        let mainMenu = NSMenu()

        let applicationItem = NSMenuItem()
        let applicationMenu = NSMenu(title: "GQY")
        applicationMenu.addItem(
            withTitle: "关于 GQY",
            action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)),
            keyEquivalent: ""
        )
        applicationMenu.addItem(.separator())
        applicationMenu.addItem(
            withTitle: "隐藏 GQY",
            action: #selector(NSApplication.hide(_:)),
            keyEquivalent: "h"
        )
        let hideOthers = applicationMenu.addItem(
            withTitle: "隐藏其他",
            action: #selector(NSApplication.hideOtherApplications(_:)),
            keyEquivalent: "h"
        )
        hideOthers.keyEquivalentModifierMask = [.command, .option]
        applicationMenu.addItem(
            withTitle: "全部显示",
            action: #selector(NSApplication.unhideAllApplications(_:)),
            keyEquivalent: ""
        )
        applicationMenu.addItem(.separator())
        let quit = applicationMenu.addItem(
            withTitle: "退出 GQY",
            action: #selector(quitApp(_:)),
            keyEquivalent: "q"
        )
        quit.target = self
        applicationItem.submenu = applicationMenu
        mainMenu.addItem(applicationItem)

        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "撤销", action: #selector(undoAction(_:)), keyEquivalent: "z").target = self
        let redo = editMenu.addItem(withTitle: "重做", action: #selector(redoAction(_:)), keyEquivalent: "z")
        redo.target = self
        redo.keyEquivalentModifierMask = [.command, .shift]
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "复制", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu
        mainMenu.addItem(editItem)

        let viewItem = NSMenuItem()
        let viewMenu = NSMenu(title: "显示")
        let reload = viewMenu.addItem(
            withTitle: "重新载入",
            action: #selector(reloadPage(_:)),
            keyEquivalent: "r"
        )
        reload.target = self
        viewItem.submenu = viewMenu
        mainMenu.addItem(viewItem)

        let windowItem = NSMenuItem()
        let windowMenu = NSMenu(title: "窗口")
        windowMenu.addItem(
            withTitle: "最小化",
            action: #selector(NSWindow.performMiniaturize(_:)),
            keyEquivalent: "m"
        )
        windowMenu.addItem(
            withTitle: "缩放",
            action: #selector(NSWindow.performZoom(_:)),
            keyEquivalent: ""
        )
        windowMenu.addItem(.separator())
        let close = windowMenu.addItem(
            withTitle: "关闭窗口",
            action: #selector(hideWindow(_:)),
            keyEquivalent: "w"
        )
        close.target = self
        windowItem.submenu = windowMenu
        mainMenu.addItem(windowItem)

        NSApp.mainMenu = mainMenu
    }

    // MARK: - 托盘

    private func buildTray() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            let image = NSImage(systemSymbolName: "sparkles", accessibilityDescription: "GQY")
            image?.isTemplate = true
            button.image = image
            button.toolTip = "GQY"
            button.target = self
            button.action = #selector(trayClicked(_:))
            button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }
        let menu = NSMenu()
        let show = menu.addItem(withTitle: "显示 GQY", action: #selector(showWindow(_:)), keyEquivalent: "")
        show.target = self
        menu.addItem(.separator())
        let quit = menu.addItem(withTitle: "退出 GQY", action: #selector(quitApp(_:)), keyEquivalent: "")
        quit.target = self
        statusMenu = menu
        statusItem = item
    }

    private func removeTray() {
        if let statusItem {
            NSStatusBar.system.removeStatusItem(statusItem)
        }
        statusItem = nil
        statusMenu = nil
    }

    @objc private func trayClicked(_ sender: Any?) {
        let event = NSApp.currentEvent
        if event?.type == .rightMouseUp {
            guard let menu = statusMenu, let button = statusItem?.button else { return }
            menu.popUp(positioning: nil, at: NSPoint(x: 0, y: button.bounds.height + 4), in: button)
        } else {
            showWindow(nil)
        }
    }

    // MARK: - 窗口

    private func buildWindow() {
        let configuration = WKWebViewConfiguration()
        configuration.preferences.setValue(true, forKey: "developerExtrasEnabled")
        configuration.websiteDataStore = .default()

        webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.setValue(false, forKey: "drawsBackground")
        webView.allowsBackForwardNavigationGestures = false

        let frame = savedFrame() ?? firstFrame()
        window = NSWindow(
            contentRect: frame,
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "GQY"
        window.minSize = NSSize(width: 400, height: 300)
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.contentView = webView
        if savedFrame() == nil {
            window.center()
        }
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(webView)
    }

    private func savedFrame() -> NSRect? {
        guard let text = defaults.string(forKey: frameKey) else { return nil }
        let rect = NSRectFromString(text)
        guard rect.width >= 400, rect.height >= 300 else { return nil }
        return clamp(rect)
    }

    /// 首次开窗的大小照 Miyu：屏幕的七成五，不小于 1100×760，不超过九成，居中；以后照上次的。
    private func firstFrame() -> NSRect {
        let screen = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        let width = min(max(screen.width * 0.75, 1100), screen.width * 0.9)
        let height = min(max(screen.height * 0.75, 760), screen.height * 0.9)
        return NSRect(
            x: screen.midX - width / 2,
            y: screen.midY - height / 2,
            width: width,
            height: height
        )
    }

    /// 上次的窗口位置落到屏幕外时拉回来，别让窗口找不着。
    private func clamp(_ frame: NSRect) -> NSRect {
        guard let screen = NSScreen.main?.visibleFrame else { return frame }
        var rect = frame
        rect.size.width = min(rect.width, screen.width)
        rect.size.height = min(rect.height, screen.height)
        rect.origin.x = min(max(rect.origin.x, screen.minX), screen.maxX - rect.width)
        rect.origin.y = min(max(rect.origin.y, screen.minY), screen.maxY - rect.height)
        return rect
    }

    private func saveFrame() {
        guard !isApplyingFrame, let window, window.isVisible else { return }
        defaults.set(NSStringFromRect(window.frame), forKey: frameKey)
    }

    func windowDidMove(_ notification: Notification) {
        saveFrame()
    }

    func windowDidResize(_ notification: Notification) {
        saveFrame()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        hideWindow(nil)
        return false
    }

    @objc private func showWindow(_ sender: Any?) {
        guard let window else { return }
        if window.isMiniaturized {
            window.deminiaturize(nil)
        }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        window.makeFirstResponder(webView)
    }

    @objc private func hideWindow(_ sender: Any?) {
        saveFrame()
        window?.orderOut(nil)
    }

    // MARK: - 服务

    private func startServer() {
        guard let port = GQYServer.freePort() else {
            showStartupError("找不到可用端口。")
            return
        }
        serverPort = port
        let process = GQYServer()
        server = process
        do {
            try process.start(port: port)
        } catch {
            server = nil
            showStartupError(error.localizedDescription)
            return
        }
        process.waitUntilHealthy(port: port) { [weak self] healthy in
            guard let self else { return }
            if healthy {
                self.loadUI()
            } else {
                self.showStartupError("GQY 服务没有在预期时间内启动。日志在 ~/Library/Logs/GQY/server.log。")
            }
        }
    }

    private func retryStartup() {
        let old = server
        server = nil
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            old?.stop(timeout: 3)
            DispatchQueue.main.async { [weak self] in
                self?.startServer()
            }
        }
    }

    private func loadUI() {
        guard let url = URL(string: "http://127.0.0.1:\(serverPort)/") else { return }
        log("loading page")
        webView.load(URLRequest(url: url))
    }

    @objc private func reloadPage(_ sender: Any?) {
        if webView.url == nil {
            loadUI()
        } else {
            webView.reload()
        }
    }

    @objc private func quitApp(_ sender: Any?) {
        NSApp.terminate(nil)
    }

    @objc private func undoAction(_ sender: Any?) {
        NSApp.sendAction(Selector(("undo:")), to: nil, from: sender)
    }

    @objc private func redoAction(_ sender: Any?) {
        NSApp.sendAction(Selector(("redo:")), to: nil, from: sender)
    }

    // MARK: - 导航

    private func isAppURL(_ url: URL) -> Bool {
        guard url.scheme?.lowercased() == "http" else { return false }
        guard let host = url.host?.lowercased(), host == "127.0.0.1" || host == "localhost" else {
            return false
        }
        return url.port == Int(serverPort)
    }

    private func openOutside(_ url: URL) {
        switch url.scheme?.lowercased() {
        case "http", "https", "mailto":
            log("opening an external \(url.scheme ?? "") link")
            NSWorkspace.shared.open(url)
        default:
            log("not opening a \(url.scheme ?? "unknown") link")
        }
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard let url = navigationAction.request.url else {
            decisionHandler(.allow)
            return
        }
        if isAppURL(url) {
            decisionHandler(.allow)
            return
        }
        switch url.scheme?.lowercased() {
        case "about", "blob", "data":
            decisionHandler(.allow)
        case "http", "https", "mailto":
            openOutside(url)
            decisionHandler(.cancel)
        default:
            log("blocked a navigation with an unsupported scheme")
            decisionHandler(.cancel)
        }
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void
    ) {
        if navigationResponse.canShowMIMEType {
            decisionHandler(.allow)
        } else {
            decisionHandler(.download)
        }
    }

    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        if let url = navigationAction.request.url {
            openOutside(url)
        }
        return nil
    }

    func webView(
        _ webView: WKWebView,
        navigationResponse: WKNavigationResponse,
        didBecome download: WKDownload
    ) {
        download.delegate = self
        activeDownloads.append(download)
    }

    func webView(
        _ webView: WKWebView,
        navigationAction: WKNavigationAction,
        didBecome download: WKDownload
    ) {
        download.delegate = self
        activeDownloads.append(download)
    }

    // MARK: - 下载

    func download(
        _ download: WKDownload,
        decideDestinationUsing response: URLResponse,
        suggestedFilename: String,
        completionHandler: @escaping (URL?) -> Void
    ) {
        guard let downloads = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first else {
            log("no downloads directory, download refused")
            completionHandler(nil)
            return
        }
        let name = Self.downloadName(suggestedFilename)
        let destination = Self.uniqueURL(in: downloads, name: name) {
            FileManager.default.fileExists(atPath: $0.path)
        }
        log("download to \(destination.lastPathComponent)")
        completionHandler(destination)
    }

    func downloadDidFinish(_ download: WKDownload) {
        activeDownloads.removeAll { $0 === download }
        log("download finished")
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        activeDownloads.removeAll { $0 === download }
        log("download failed: \(error.localizedDescription)")
    }

    /// 建议的文件名只留最后一段，去掉路径分隔符和空白；空的叫 `download`。
    static func downloadName(_ suggested: String) -> String {
        var name = (suggested as NSString).lastPathComponent
        name = name.replacingOccurrences(of: "/", with: "-")
        name = name.replacingOccurrences(of: ":", with: "-")
        name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "download" : name
    }

    /// 重名加「 (1)」「 (2)」；`exists` 说某个地址在不在。
    static func uniqueURL(in directory: URL, name: String, exists: (URL) -> Bool) -> URL {
        let first = directory.appendingPathComponent(name)
        if !exists(first) { return first }
        let ext = (name as NSString).pathExtension
        let base = (name as NSString).deletingPathExtension
        var index = 1
        while true {
            let candidate = ext.isEmpty ? "\(base) (\(index))" : "\(base) (\(index)).\(ext)"
            let url = directory.appendingPathComponent(candidate)
            if !exists(url) { return url }
            index += 1
        }
    }

    // MARK: - 错误

    private func showStartupError(_ message: String) {
        guard !isShowingAlert else { return }
        isShowingAlert = true
        let alert = NSAlert()
        alert.messageText = "GQY 没有启动"
        alert.informativeText = message
        alert.addButton(withTitle: "重试")
        alert.addButton(withTitle: "退出")
        let response = alert.runModal()
        isShowingAlert = false
        if response == .alertFirstButtonReturn {
            retryStartup()
        } else {
            NSApp.terminate(nil)
        }
    }

    private func showPageError(_ error: Error) {
        let code = (error as NSError).code
        if code == NSURLErrorCancelled { return }
        guard !isShowingAlert else { return }
        isShowingAlert = true
        let alert = NSAlert()
        alert.messageText = "打不开 GQY 的页面。"
        alert.informativeText = error.localizedDescription
        alert.addButton(withTitle: "重试")
        alert.addButton(withTitle: "退出")
        let response = alert.runModal()
        isShowingAlert = false
        if response == .alertFirstButtonReturn {
            reloadPage(nil)
        } else {
            NSApp.terminate(nil)
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        showPageError(error)
    }

    func webView(
        _ webView: WKWebView,
        didFailProvisionalNavigation navigation: WKNavigation!,
        withError error: Error
    ) {
        showPageError(error)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        log("web content process terminated, reloading")
        webView.reload()
    }

    // MARK: - 日志

    private func openDesktopLog() {
        let directory = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/GQY", isDirectory: true)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let path = directory.appendingPathComponent(desktopLogName)
        if !FileManager.default.fileExists(atPath: path.path) {
            FileManager.default.createFile(atPath: path.path, contents: nil)
        }
        desktopLog = try? FileHandle(forWritingTo: path)
        desktopLog?.seekToEndOfFile()
    }

    private func log(_ line: String) {
        let formatter = ISO8601DateFormatter()
        let text = "\(formatter.string(from: Date())) \(line)\n"
        if let data = text.data(using: .utf8) {
            try? desktopLog?.write(contentsOf: data)
        }
    }
}
