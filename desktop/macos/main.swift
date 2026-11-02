import Cocoa
import Darwin
import WebKit

let appDelegate = AppDelegate()
let application = NSApplication.shared
application.delegate = appDelegate
application.setActivationPolicy(.regular)
application.run()

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var server: Process?
    private var serverPort: UInt16 = 0
    private var logHandle: FileHandle?

    func applicationDidFinishLaunching(_ notification: Notification) {
        buildMenu()
        buildWindow()

        guard let port = freePort() else {
            showError("找不到可用端口。")
            return
        }
        serverPort = port
        do {
            try startServer(port: port)
        } catch {
            showError("启动 GQY 服务失败：\(error.localizedDescription)")
            return
        }
        waitForServer(port: port, attempts: 100) { [weak self] ok in
            guard let self else { return }
            if ok {
                self.loadUI()
            } else {
                self.showError("GQY 服务没有在预期时间内启动。日志在 ~/Library/Logs/GQY/server.log。")
            }
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    private func buildMenu() {
        let mainMenu = NSMenu()

        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 GQY", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "隐藏 GQY", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(withTitle: "退出 GQY", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        mainMenu.addItem(appItem)

        let viewItem = NSMenuItem()
        let viewMenu = NSMenu(title: "显示")
        viewMenu.addItem(withTitle: "重新载入", action: #selector(reloadPage(_:)), keyEquivalent: "r")
        viewItem.submenu = viewMenu
        mainMenu.addItem(viewItem)

        let windowItem = NSMenuItem()
        let windowMenu = NSMenu(title: "窗口")
        windowMenu.addItem(withTitle: "最小化", action: #selector(NSWindow.miniaturize(_:)), keyEquivalent: "m")
        windowMenu.addItem(withTitle: "缩放", action: #selector(NSWindow.zoom(_:)), keyEquivalent: "")
        windowItem.submenu = windowMenu
        mainMenu.addItem(windowItem)

        NSApp.mainMenu = mainMenu
    }

    private func buildWindow() {
        let config = WKWebViewConfiguration()
        config.preferences.setValue(true, forKey: "developerExtrasEnabled")
        webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self
        webView.setValue(false, forKey: "drawsBackground")

        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "GQY"
        window.minSize = NSSize(width: 900, height: 600)
        window.center()
        window.contentView = webView
        window.makeKeyAndOrderFront(nil)
    }

    private func loadUI() {
        guard let url = URL(string: "http://127.0.0.1:\(serverPort)/") else { return }
        webView.load(URLRequest(url: url))
    }

    @objc private func reloadPage(_ sender: Any?) {
        webView.reload()
    }

    private func startServer(port: UInt16) throws {
        guard let resourceURL = Bundle.main.resourceURL else {
            throw NSError(domain: "GQY", code: 1, userInfo: [NSLocalizedDescriptionKey: "缺少应用资源目录"])
        }
        let binary = resourceURL.appendingPathComponent("bin/gqy-server")
        let webDir = resourceURL.appendingPathComponent("web")
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw NSError(domain: "GQY", code: 2, userInfo: [NSLocalizedDescriptionKey: "缺少 gqy-server 可执行文件"])
        }

        let home = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".gqy")
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])

        let logDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/GQY", isDirectory: true)
        try FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        let logPath = logDir.appendingPathComponent("server.log")
        if !FileManager.default.fileExists(atPath: logPath.path) {
            FileManager.default.createFile(atPath: logPath.path, contents: nil)
        }
        logHandle = try FileHandle(forWritingTo: logPath)
        logHandle?.seekToEndOfFile()

        let process = Process()
        process.executableURL = binary
        var env = ProcessInfo.processInfo.environment
        env["HOST"] = "127.0.0.1"
        env["PORT"] = String(port)
        env["GQY_HOME"] = home.path
        env["GQY_WEB_DIR"] = webDir.path
        process.environment = env
        process.standardOutput = logHandle
        process.standardError = logHandle
        try process.run()
        server = process
    }

    private func stopServer() {
        guard let process = server, process.isRunning else { return }
        process.terminate()
        process.waitUntilExit()
        server = nil
        try? logHandle?.close()
        logHandle = nil
    }

    private func waitForServer(port: UInt16, attempts: Int, completion: @escaping (Bool) -> Void) {
        guard attempts > 0 else {
            completion(false)
            return
        }
        guard let url = URL(string: "http://127.0.0.1:\(port)/healthz") else {
            completion(false)
            return
        }
        var request = URLRequest(url: url)
        request.timeoutInterval = 0.6
        URLSession.shared.dataTask(with: request) { [weak self] _, response, _ in
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                DispatchQueue.main.async { completion(true) }
                return
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                self?.waitForServer(port: port, attempts: attempts - 1, completion: completion)
            }
        }.resume()
    }

    private func showError(_ message: String) {
        let html = """
        <!doctype html><meta charset="utf-8">
        <style>
          body{margin:0;height:100vh;display:grid;place-items:center;background:#f2ece2;color:#221c17;
               font:14px -apple-system,"PingFang SC",sans-serif}
          .card{max-width:560px;padding:28px;border:1px solid #d9cfbf;border-radius:20px;background:#fbf7ef;
                box-shadow:0 24px 80px rgba(38,30,22,.18)}
          h1{margin:0 0 10px;font:600 22px "Iowan Old Style","Songti SC",serif}
          p{margin:0;color:#71665a;line-height:1.65}
          code{font-family:ui-monospace,Menlo,monospace;font-size:12px}
        </style>
        <div class="card"><h1>GQY 没有启动</h1><p>\(message)</p></div>
        """
        webView.loadHTMLString(html, baseURL: nil)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = navigationAction.request.url else {
            decisionHandler(.allow)
            return
        }
        let host = url.host ?? ""
        if host != "127.0.0.1" && host != "localhost" {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        showError("页面加载失败：\(error.localizedDescription)")
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        showError("页面加载失败：\(error.localizedDescription)")
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func applicationWillTerminate(_ notification: Notification) {
        stopServer()
    }

    private func freePort() -> UInt16? {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return nil }
        defer { close(fd) }

        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = 0
        address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))

        let bindResult = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bindResult == 0 else { return nil }

        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        let nameResult = withUnsafeMutablePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(fd, $0, &length)
            }
        }
        guard nameResult == 0 else { return nil }
        return UInt16(bigEndian: address.sin_port)
    }
}
