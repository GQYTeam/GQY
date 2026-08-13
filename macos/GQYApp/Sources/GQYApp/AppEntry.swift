import SwiftUI
import AppKit

/// 应用退出时终止后端进程：避免 gqy 残留占 4096 端口，重开 App 才能正常拉起
final class AppDelegate: NSObject, NSApplicationDelegate {
    var onTerminate: (() -> Void)?

    func applicationWillTerminate(_ notification: Notification) {
        onTerminate?()
    }
}

@main
struct GQYApp: App {
    @StateObject private var vm: ShellViewModel
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    init() {
        let port = Int(ProcessInfo.processInfo.environment["GQY_WEB_PORT"] ?? "") ?? 4096
        let local = URL(string: "http://127.0.0.1:\(port)")!
        // 配置了远程服务器地址（设置里填写）→ 直接连远程，不拉起本地后端
        let remote = UserDefaults.standard.string(forKey: "remoteURL")?
            .trimmingCharacters(in: .whitespaces)
        _vm = StateObject(wrappedValue: ShellViewModel(
            baseURL: remote.flatMap { URL(string: $0) } ?? local
        ))
    }

    var body: some Scene {
        WindowGroup("顾清影") {
            ContentView()
                .environmentObject(vm)
                .onAppear {
                    appDelegate.onTerminate = { [weak vm] in
                        vm?.stopBackend()
                    }
                    vm.start()
                }
        }
        .defaultSize(width: 1080, height: 720)
    }
}
