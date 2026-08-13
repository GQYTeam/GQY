import SwiftUI
import AppKit
import WebKit

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

        // 悬浮窗模式：置顶小卡片（复用 WebUI 的 ?panel=1 单聊形态）
        Window("顾清影 · 悬浮窗", id: "panel") {
            PanelShellView(baseURL: vm.baseURL)
                .frame(minWidth: 380, minHeight: 520)
        }
        .defaultSize(width: 420, height: 560)
        .commands {
            CommandMenu("顾清影") {
                PanelWindowButton()
            }
        }
    }
}

/// 菜单按钮：打开悬浮窗
struct PanelWindowButton: View {
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Button("打开悬浮窗") {
            openWindow(id: "panel")
        }
    }
}

/// 悬浮窗：WebUI 的 panel 形态（隐藏侧栏/顶栏，单聊卡片）
struct PanelShellView: NSViewRepresentable {
    let baseURL: URL

    func makeNSView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero)
        var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)!
        var items = components.queryItems ?? []
        items.append(URLQueryItem(name: "panel", value: "1"))
        components.queryItems = items
        webView.load(URLRequest(url: components.url!))
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {}
}
