import SwiftUI
import WebKit

// MARK: - 根视图：离线 → 连接中 → WebUI（WKWebView）

struct ContentView: View {
    @EnvironmentObject var vm: ShellViewModel
    @State private var showSettings = false

    var body: some View {
        Group {
            switch vm.connection {
            case .offline: OfflineView()
            case .connecting: ProgressView("连接顾清影…")
            case .ready: WebShellView(url: vm.baseURL)
            }
        }
        .id(vm.baseURL) // 切换远程/本地地址时重建 WebView
        .frame(minWidth: 820, minHeight: 600)
        .tint(Color(red: 0.384, green: 0.784, blue: 0.569))
        .preferredColorScheme(.dark)
        .toolbar {
            ToolbarItem(placement: .navigation) {
                Button {
                    showSettings = true
                } label: {
                    Label("设置", systemImage: "gearshape")
                }
                .help("连接设置")
            }
        }
        .sheet(isPresented: $showSettings) {
            SettingsView()
                .environmentObject(vm)
        }
        .alert("提示", isPresented: Binding(
            get: { vm.message != nil },
            set: { if !$0 { vm.message = nil } }
        )) {
            Button("好") { vm.message = nil }
        } message: {
            Text(vm.message ?? "")
        }
    }
}

// MARK: - 未连接

struct OfflineView: View {
    @EnvironmentObject var vm: ShellViewModel

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: "power.circle")
                .font(.system(size: 56))
                .foregroundStyle(.secondary)
            Text("顾清影还没醒")
                .font(.title2)
            Text("将启动本地 gqy web 服务（127.0.0.1:4096）")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack(spacing: 12) {
                Button {
                    vm.startBackend()
                } label: {
                    Label("唤醒她", systemImage: "power")
                }
                .buttonStyle(.borderedProminent)
                Button("重试连接") { vm.start() }
                    .buttonStyle(.bordered)
            }
        }
        .padding(40)
    }
}

struct WebShellView: NSViewRepresentable {
    let url: URL

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero)
        webView.allowsMagnification = true
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {}

    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
        /// target=_blank（图片「在新窗口打开」等）→ 独立预览窗口，聊天窗口不受影响、可随时关闭
        func webView(
            _ webView: WKWebView,
            createWebViewWith configuration: WKWebViewConfiguration,
            for navigationAction: WKNavigationAction,
            windowFeatures: WKWindowFeatures
        ) -> WKWebView? {
            guard let request = navigationAction.request as URLRequest? else { return nil }
            let size = NSSize(
                width: windowFeatures.width?.doubleValue ?? 800,
                height: windowFeatures.height?.doubleValue ?? 640
            )
            let window = NSWindow(
                contentRect: NSRect(origin: .zero, size: size),
                styleMask: [.titled, .closable, .resizable, .miniaturizable],
                backing: .buffered,
                defer: false
            )
            window.title = "顾清影 · 预览"
            let preview = WKWebView(frame: NSRect(origin: .zero, size: size), configuration: configuration)
            preview.allowsMagnification = true
            preview.navigationDelegate = self
            preview.uiDelegate = self
            window.contentView = preview
            window.center()
            window.makeKeyAndOrderFront(nil)
            preview.load(request)
            return preview
        }

        // MARK: - 下载 / 导出（a[download] 链接，如导出对话、图片保存）

        func webView(
            _ webView: WKWebView,
            decidePolicyFor navigationAction: WKNavigationAction,
            decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
        ) {
            // 带 download 属性的链接（图片保存、导出对话等）→ 走 WKDownload 保存
            if navigationAction.shouldPerformDownload {
                decisionHandler(.download)
                return
            }
            decisionHandler(.allow)
        }

        func webView(
            _ webView: WKWebView,
            navigationAction: WKNavigationAction,
            didBecome download: WKDownload
        ) {
            download.delegate = self
        }

        func download(
            _ download: WKDownload,
            decideDestinationUsing response: URLResponse,
            suggestedFilename: String,
            completionHandler: @escaping (URL?) -> Void
        ) {
            let downloads = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Downloads", isDirectory: true)
            try? FileManager.default.createDirectory(at: downloads, withIntermediateDirectories: true)
            let unique = uniqueDestination(in: downloads, filename: suggestedFilename)
            completionHandler(unique)
        }

        func downloadDidFinish(_ download: WKDownload) {
            // WKDownload 不暴露最终路径，改用系统提示音 + 打开下载目录兜底
            NSSound.beep()
        }

        private func uniqueDestination(in dir: URL, filename: String) -> URL {
            var candidate = dir.appendingPathComponent(filename)
            let name = (filename as NSString).deletingPathExtension
            let ext = (filename as NSString).pathExtension
            var counter = 1
            while FileManager.default.fileExists(atPath: candidate.path) {
                let suffixed = "\(name)-\(counter)"
                candidate = dir.appendingPathComponent(ext.isEmpty ? suffixed : "\(suffixed).\(ext)")
                counter += 1
            }
            return candidate
        }
    }
}
