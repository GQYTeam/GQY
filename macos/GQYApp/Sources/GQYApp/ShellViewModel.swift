import Foundation

/// 默认数据目录（与后端 default_isolated_home 一致）
enum HomePath {
    static var value: String {
        if let env = ProcessInfo.processInfo.environment["GQY_HOME"], !env.isEmpty {
            return env
        }
        return NSHomeDirectory() + "/Library/Application Support/gqy"
    }
}

/// 壳层状态：探活 + 一键拉起 gqy web。聊天全部交给 WKWebView 里的 WebUI。
@MainActor
final class ShellViewModel: ObservableObject {
    enum Connection { case offline, connecting, ready }

    @Published var connection: Connection = .offline
    @Published var message: String?
    /// QQ 机器人开关（设置里切换；开启时启动 gqy qq 子进程，与 App 同启停）
    @Published var qqEnabled: Bool {
        didSet {
            UserDefaults.standard.set(qqEnabled, forKey: "qqEnabled")
            if qqEnabled != oldValue {
                if qqEnabled {
                    startQqProcess()
                } else {
                    stopQqProcess()
                }
            }
        }
    }

    var baseURL: URL
    private var client: APIClient
    private var healthTask: Task<Void, Never>?
    private var backendProcess: Process?
    private var qqProcess: Process?
    private var lanMode = false
    private var lanPassword: String?

    /// 远程模式：baseURL 指向非本机地址（设置里填的服务器），不拉起本地后端
    var remoteMode: Bool {
        guard let host = baseURL.host else { return true }
        return host != "127.0.0.1" && host != "localhost"
    }

    /// App 退出时终止后端进程，避免 gqy 残留占端口（否则重开 App 拉不起新进程）
    func stopBackend() {
        healthTask?.cancel()
        healthTask = nil
        if let process = backendProcess, process.isRunning {
            process.terminate()
        }
        backendProcess = nil
        stopQqProcess()
    }

    /// 启动 gqy qq 子进程（反向 WebSocket 监听，与 App 同生死）
    func startQqProcess() {
        guard !remoteMode else { return }
        if let process = qqProcess, process.isRunning { return }
        var candidates: [String] = []
        if let bundled = Bundle.main.path(forResource: "gqy", ofType: nil) {
            candidates.append(bundled)
        }
        candidates += [
            ProcessInfo.processInfo.environment["GQY_BIN"],
            "/opt/homebrew/bin/gqy",
            "/usr/local/bin/gqy",
        ]
        .compactMap { $0 }
        .filter { FileManager.default.isExecutableFile(atPath: $0) }
        guard let binary = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) else {
            return
        }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = ["qq"]
        var env = ProcessInfo.processInfo.environment
        if env["GQY_HOME"] == nil {
            env["GQY_HOME"] = HomePath.value
        }
        process.environment = env
        do {
            try process.run()
            qqProcess = process
        } catch {
            message = "启动 QQ 后端失败：\(error.localizedDescription)"
        }
    }

    /// 终止 gqy qq 子进程（开关关闭 / App 退出时）
    func stopQqProcess() {
        if let process = qqProcess, process.isRunning {
            process.terminate()
        }
        qqProcess = nil
    }

    /// 局域网 IPv4（ifconfig 解析，优先 192.168/10./172.16 段）
    private static func lanIP() -> String {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/sbin/ifconfig")
        task.arguments = ["-l"]
        let pipe = Pipe()
        task.standardOutput = pipe
        try? task.run()
        task.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let interfaces = String(data: data, encoding: .utf8) ?? ""
        for name in interfaces.split(separator: " ") {
            let t = Process()
            t.executableURL = URL(fileURLWithPath: "/sbin/ifconfig")
            t.arguments = [String(name)]
            let p = Pipe()
            t.standardOutput = p
            try? t.run()
            t.waitUntilExit()
            let out = String(data: p.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            for line in out.split(separator: "\n") {
                let l = line.trimmingCharacters(in: .whitespaces)
                if l.hasPrefix("inet ") {
                    let ip = l.split(separator: " ")[1]
                    let s = String(ip)
                    if s.hasPrefix("192.168.") || s.hasPrefix("10.") || s.hasPrefix("172.") {
                        return s
                    }
                }
            }
        }
        return "127.0.0.1"
    }

    init(baseURL: URL) {
        self.baseURL = baseURL
        client = APIClient(baseURL: baseURL)
        _qqEnabled = Published(initialValue: UserDefaults.standard.bool(forKey: "qqEnabled"))
    }

    func start() {
        startBackend()
        if qqEnabled {
            startQqProcess()
        }
    }

    /// 切换远程/本地地址并重连（设置里保存后调用）
    func applyRemote(urlString: String) {
        UserDefaults.standard.set(urlString, forKey: "remoteURL")
        let trimmed = urlString.trimmingCharacters(in: .whitespaces)
        baseURL = trimmed.isEmpty ? URL(string: "http://127.0.0.1:4096")! : (URL(string: trimmed) ?? URL(string: "http://127.0.0.1:4096")!)
        client = APIClient(baseURL: baseURL)
        stopBackend()
        connection = .offline
        message = nil
        start()
    }

    func startBackend() {
        // 已有服务在跑（残留/外部启动）→ 直接复用，不再拉起新进程
        Task { [weak self] in
            guard let self else { return }
            if await self.client.health() {
                self.connection = .ready
                return
            }
            // 远程模式：不拉起本地进程，探活失败直接报错
            if self.remoteMode {
                self.connection = .offline
                self.message = "远程服务器 \(self.baseURL.absoluteString) 连不上，检查地址或服务器状态"
                return
            }
            self.launchBackendProcess()
        }
    }

    private func launchBackendProcess() {
        // 一体化优先：内嵌二进制（.app/Contents/Resources/gqy）
        var candidates: [String] = []
        if let bundled = Bundle.main.path(forResource: "gqy", ofType: nil) {
            candidates.append(bundled)
        }
        candidates += [
            ProcessInfo.processInfo.environment["GQY_BIN"],
            "/opt/homebrew/bin/gqy",
            "/usr/local/bin/gqy",
        ]
        .compactMap { $0 }
        .filter { FileManager.default.isExecutableFile(atPath: $0) }
        guard let binary = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) else {
            message = "找不到 gqy 后端：请设置 GQY_BIN 环境变量，或 brew install gqy"
            return
        }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: binary)
        var arguments = ["web", "--no-open"]
        // 局域网模式：配置了 web_ui.password 时监听 0.0.0.0，手机可访问
        if let password = configuredWebPassword(), !password.isEmpty {
            lanMode = true
            lanPassword = password
            arguments += ["--host", "0.0.0.0", "-p", password]
        } else {
            lanMode = false
        }
        process.arguments = arguments
        var env = ProcessInfo.processInfo.environment
        if env["GQY_HOME"] == nil {
            env["GQY_HOME"] = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Library/Application Support/gqy").path
        }
        process.environment = env
        do {
            try process.run()
            backendProcess = process
            connection = .connecting
            waitForBackend()
        } catch {
            message = "启动后端失败：\(error.localizedDescription)"
        }
    }

    /// 从 GQY_HOME/config/config.jsonc 读取 web_ui.password（轻量 JSON 扫描，不依赖后端）
    private func configuredWebPassword() -> String? {
        let home = ProcessInfo.processInfo.environment["GQY_HOME"]
            ?? FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Library/Application Support/gqy").path
        let file = URL(fileURLWithPath: home).appendingPathComponent("config/config.jsonc")
        guard let text = try? String(contentsOf: file, encoding: .utf8) else { return nil }
        // 去掉 // 行注释后匹配 "web_ui" 段里的 password
        let stripped = text
            .split(separator: "\n")
            .filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("//") }
            .joined(separator: "\n")
        guard let range = stripped.range(of: "\"web_ui\"") else { return nil }
        let segment = stripped[range.lowerBound...].prefix(400)
        let pattern = "\"password\"\\s*:\\s*\"([^\"]*)\""
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return nil }
        let segmentText = String(segment)
        let match = regex.firstMatch(
            in: segmentText, range: NSRange(segmentText.startIndex..., in: segmentText)
        )
        guard let match, let r = Range(match.range(at: 1), in: segmentText) else { return nil }
        return String(segmentText[r])
    }

    private func waitForBackend() {
        healthTask?.cancel()
        healthTask = Task { [weak self] in
            guard let self else { return }
            for _ in 0..<150 {
                try? await Task.sleep(nanoseconds: 200_000_000)
                if Task.isCancelled { return }
                if await self.client.health() {
                    self.connection = .ready
                    if self.lanMode {
                        let ip = Self.lanIP()
                        let port = ProcessInfo.processInfo.environment["GQY_WEB_PORT"] ?? "4096"
                        self.message = "📱 手机访问（同一 Wi-Fi）：\nhttp://\(ip):\(port)\n密码：\(self.lanPassword ?? "")"
                    } else if self.remoteMode {
                        self.message = "已连接远程：\(self.baseURL.absoluteString)"
                    }
                    return
                }
            }
            self.connection = .offline
            self.message = "后端启动超时"
        }
    }
}
