import SwiftUI

@main
struct GQYApp: App {
    @StateObject private var vm: ShellViewModel

    init() {
        let port = Int(ProcessInfo.processInfo.environment["GQY_WEB_PORT"] ?? "") ?? 4096
        _vm = StateObject(wrappedValue: ShellViewModel(baseURL: URL(string: "http://127.0.0.1:\(port)")!))
    }

    var body: some Scene {
        WindowGroup("顾清影") {
            ContentView()
                .environmentObject(vm)
                .onAppear { vm.start() }
        }
        .defaultSize(width: 1080, height: 720)
    }
}
