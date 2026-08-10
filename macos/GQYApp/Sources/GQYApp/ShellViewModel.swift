import Foundation

/// 壳层状态：探活 + 一键拉起 gqy web。聊天全部交给 WKWebView 里的 WebUI。
@MainActor
final class ShellViewModel: ObservableObject {
    enum Connection { case offline, connecting, ready }

    @Published var connection: Connection = .offline
    @Published var message: String?

    let baseURL: URL
    private let client: APIClient
    private var healthTask: Task<Void, Never>?
    private var backendProcess: Process?
    private var lanMode = false
    private var lanPassword: String?

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
    }

    func start() {
        startBackend()
    }

    func startBackend() {
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
                    }
                    return
                }
            }
            self.connection = .offline
            self.message = "后端启动超时"
        }
    }
}
