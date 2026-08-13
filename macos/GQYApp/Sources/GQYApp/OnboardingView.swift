import SwiftUI

/// 首次启动引导：欢迎 + 打开方式 + 数据位置 + 备份提醒
struct OnboardingView: View {
    var onContinue: () -> Void
    @State private var homePath = ProcessInfo.processInfo.environment["GQY_HOME"]
        ?? NSHomeDirectory() + "/Library/Application Support/gqy"

    var body: some View {
        VStack(spacing: 24) {
            Spacer()

            ZStack {
                Circle()
                    .fill(Color(red: 0.384, green: 0.784, blue: 0.569).opacity(0.12))
                    .frame(width: 120, height: 120)
                Image(systemName: "moon.stars.fill")
                    .font(.system(size: 52))
                    .foregroundStyle(Color(red: 0.384, green: 0.784, blue: 0.569))
            }

            VStack(spacing: 6) {
                Text("你好，我是顾清影")
                    .font(.largeTitle.bold())
                Text("清冷的影子，住在你的 Mac 里。任务来了就利落，聊开了就放松。")
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }

            VStack(alignment: .leading, spacing: 12) {
                Label {
                    Text("如果打不开：右键（按住 Control）点 App 图标 → 「打开」确认一次即可。")
                } icon: {
                    Image(systemName: "cursorarrow.click.2")
                }
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("我的记忆和配置都在：")
                        Text(homePath)
                            .textSelection(.enabled)
                            .foregroundStyle(.primary)
                    }
                } icon: {
                    Image(systemName: "externaldrive")
                }
                Label {
                    Text("每轮对话后自动 Git 快照并推送到你的私有远程仓库，换机一条命令恢复。")
                } icon: {
                    Image(systemName: "arrow.triangle.2.circlepath.icloud")
                }
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            .frame(maxWidth: 460, alignment: .leading)
            .padding(18)
            .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 14))

            Button(action: onContinue) {
                Text("开始")
                    .font(.headline)
                    .padding(.horizontal, 36)
                    .padding(.vertical, 8)
            }
            .buttonStyle(.borderedProminent)
            .tint(Color(red: 0.384, green: 0.784, blue: 0.569))

            Spacer()
        }
        .frame(minWidth: 520, minHeight: 620)
        .padding(32)
    }
}
