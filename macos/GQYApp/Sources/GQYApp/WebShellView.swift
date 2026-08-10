import SwiftUI
import WebKit

// MARK: - 根视图：离线 → 连接中 → WebUI（WKWebView）

struct ContentView: View {
    @EnvironmentObject var vm: ShellViewModel

    var body: some View {
        Group {
            switch vm.connection {
            case .offline: OfflineView()
            case .connecting: ProgressView("连接顾清影…")
            case .ready: WebShellView(url: vm.baseURL)
            }
        }
        .frame(minWidth: 820, minHeight: 600)
        .tint(Color(red: 0.384, green: 0.784, blue: 0.569))
        .preferredColorScheme(.dark)
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

// MARK: - WebUI 容器

struct WebShellView: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero)
        webView.allowsMagnification = true
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {}
}
