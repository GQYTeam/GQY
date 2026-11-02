import Cocoa
import Darwin

/// Go 服务的生命周期（施工单 035）：应用启动时拉起，退出时停掉；关窗口不停。
final class GQYServer {
    private var process: Process?
    private var serverLog: FileHandle?
    private(set) var isRunning = false

    /// 拉起 `Resources/bin/gqy-server`，日志接 `~/Library/Logs/GQY/server.log`。
    func start(port: UInt16) throws {
        guard let resourceURL = Bundle.main.resourceURL else {
            throw GQYServerError.message("缺少应用资源目录")
        }
        let binary = resourceURL.appendingPathComponent("bin/gqy-server")
        let webDir = resourceURL.appendingPathComponent("web")
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw GQYServerError.message("缺少 gqy-server 可执行文件")
        }
        guard FileManager.default.fileExists(atPath: webDir.path) else {
            throw GQYServerError.message("缺少 web 资源目录")
        }

        let home = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".gqy")
        try FileManager.default.createDirectory(
            at: home,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )

        let logDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/GQY", isDirectory: true)
        try FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        let logPath = logDir.appendingPathComponent("server.log")
        if !FileManager.default.fileExists(atPath: logPath.path) {
            FileManager.default.createFile(atPath: logPath.path, contents: nil)
        }
        serverLog = try FileHandle(forWritingTo: logPath)
        serverLog?.seekToEndOfFile()

        let process = Process()
        process.executableURL = binary
        var environment = ProcessInfo.processInfo.environment
        environment["HOST"] = "127.0.0.1"
        environment["PORT"] = String(port)
        environment["GQY_HOME"] = home.path
        environment["GQY_WEB_DIR"] = webDir.path
        process.environment = environment
        process.standardOutput = serverLog
        process.standardError = serverLog
        try process.run()
        self.process = process
        isRunning = true

        process.terminationHandler = { [weak self] finished in
            DispatchQueue.main.async {
                self?.isRunning = false
                self?.appendLog("server exited with status \(finished.terminationStatus)")
            }
        }
        appendLog("server started on 127.0.0.1:\(port)")
    }

    /// 停服务：先 SIGTERM 让 Go 优雅退出，超过时限再 SIGKILL。只应在后台线程调用。
    func stop(timeout: TimeInterval = 10) {
        guard let process, process.isRunning else {
            closeLog()
            isRunning = false
            return
        }
        appendLog("stopping server")
        process.terminate()
        let deadline = Date().addingTimeInterval(timeout)
        while process.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        if process.isRunning {
            appendLog("server did not stop in \(Int(timeout))s, killing")
            kill(process.processIdentifier, SIGKILL)
        }
        process.waitUntilExit()
        self.process = nil
        isRunning = false
        closeLog()
        appendLog("server stopped")
    }

    private func closeLog() {
        try? serverLog?.close()
        serverLog = nil
    }

    private func appendLog(_ line: String) {
        let formatter = ISO8601DateFormatter()
        let text = "\(formatter.string(from: Date())) \(line)\n"
        if let data = text.data(using: .utf8) {
            try? serverLog?.write(contentsOf: data)
        }
    }

    /// 等 `/healthz` 返回 200；回调在主线程。
    func waitUntilHealthy(port: UInt16, attempts: Int = 100, completion: @escaping (Bool) -> Void) {
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
        request.cachePolicy = .reloadIgnoringLocalCacheData
        URLSession.shared.dataTask(with: request) { [weak self] _, response, _ in
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                DispatchQueue.main.async { completion(true) }
                return
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                self?.waitUntilHealthy(port: port, attempts: attempts - 1, completion: completion)
            }
        }.resume()
    }

    /// 找一个本机空闲端口。
    static func freePort() -> UInt16? {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { return nil }
        defer { close(descriptor) }

        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = 0
        address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))

        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bound == 0 else { return nil }

        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        let named = withUnsafeMutablePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(descriptor, $0, &length)
            }
        }
        guard named == 0 else { return nil }
        return UInt16(bigEndian: address.sin_port)
    }
}

enum GQYServerError: LocalizedError {
    case message(String)

    var errorDescription: String? {
        switch self {
        case .message(let text): return text
        }
    }
}
