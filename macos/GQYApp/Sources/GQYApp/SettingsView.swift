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

            Divider()

            VStack(alignment: .leading, spacing: 8) {
                Text("数据")
                    .font(.headline)
                Text("数据目录（GQY_HOME）：\n\(HomePath.value)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                Text("备份：每轮对话后自动 Git 快照并推送远程。配置与恢复指引在 WebUI「设置 → 备份」。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Divider()

            VStack(alignment: .leading, spacing: 8) {
                Text("QQ 机器人")
                    .font(.headline)
                Toggle("启用 QQ（与 App 同启停）", isOn: $vm.qqEnabled)
                Text("开启后 App 启动即拉起 gqy qq 子进程（反向 WebSocket 监听），退出即终止。NapCat 需单独运行并连入；主人 QQ 默认 1950930166。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

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
