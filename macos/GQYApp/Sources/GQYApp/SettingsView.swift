import SwiftUI

/// 连接设置：本机 gqy（默认）或远程服务器顾清影
struct SettingsView: View {
    @EnvironmentObject var vm: ShellViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var remoteURL: String

    init() {
        _remoteURL = State(initialValue: UserDefaults.standard.string(forKey: "remoteURL") ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("连接设置")
                .font(.title2.bold())
            Text("留空 = 本机模式（自动启动本地 gqy）；填写服务器地址 = 远程模式（连接服务器上的顾清影，不启动本地后端）。")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("如 https://gqy.example.com 或 http://100.64.0.1:4096", text: $remoteURL)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 380)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("保存并连接") {
                    vm.applyRemote(urlString: remoteURL)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(24)
    }
}
