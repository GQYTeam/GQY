import Cocoa
import Darwin

/// 单实例锁（施工单 035）：同一个用户下只允许一个 GQY 外壳。锁用 `flock`，进程退出自动释放。
final class InstanceLock {
    /// 第二个实例叫第一个实例显示窗口的通知名。
    static let showNotification = Notification.Name("dev.gqy.app.show-window")
    /// 应用标识。
    static let bundleIdentifier = "dev.gqy.app"

    private var descriptor: Int32 = -1
    private let path: URL

    init() {
        let support = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/GQY", isDirectory: true)
        path = support.appendingPathComponent("instance.lock")
    }

    /// 拿锁。拿不到说明已经有一个在跑；拿不了锁文件时放行，不因为文件系统问题挡住启动。
    func acquire() -> Bool {
        let directory = path.deletingLastPathComponent()
        try? FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        descriptor = open(path.path, O_CREAT | O_RDWR, 0o600)
        guard descriptor >= 0 else { return true }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            close(descriptor)
            descriptor = -1
            return false
        }
        let pid = "\(getpid())\n"
        ftruncate(descriptor, 0)
        _ = pid.withCString { write(descriptor, $0, strlen($0)) }
        return true
    }

    /// 放锁（进程退出时系统也会放）。
    func release() {
        guard descriptor >= 0 else { return }
        _ = flock(descriptor, LOCK_UN)
        close(descriptor)
        descriptor = -1
    }

    /// 第二个实例：通知第一个实例显示窗口，并把它激活到最前，然后退出。
    static func signalExisting() {
        DistributedNotificationCenter.default().post(
            name: showNotification,
            object: nil
        )
        let running = NSRunningApplication.runningApplications(withBundleIdentifier: bundleIdentifier)
            .first { $0.processIdentifier != getpid() }
        guard let running else { return }
        if #available(macOS 14.0, *) {
            running.activate()
        } else {
            running.activate(options: [.activateIgnoringOtherApps])
        }
    }
}
