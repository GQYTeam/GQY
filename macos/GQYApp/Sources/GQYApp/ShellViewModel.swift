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

    init(baseURL: URL) {
        self.baseURL = baseURL
        client = APIClient(baseURL: baseURL)
    }

    func start() {
        healthTask?.cancel()
        healthTask = Task { [weak self] in
            guard let self, !Task.isCancelled else { return }
            self.connection = .connecting
            if await self.client.health() {
                self.connection = .ready
            } else {
                self.connection = .offline
            }
        }
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
        process.arguments = ["web", "--no-open"]
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

    private func waitForBackend() {
        healthTask?.cancel()
        healthTask = Task { [weak self] in
            guard let self else { return }
            for _ in 0..<150 {
                try? await Task.sleep(nanoseconds: 200_000_000)
                if Task.isCancelled { return }
                if await self.client.health() {
                    self.connection = .ready
                    return
                }
            }
            self.connection = .offline
            self.message = "后端启动超时"
        }
    }
}
