import Cocoa

// 入口（施工单 035）：先拿单实例锁，再起 AppKit。第二个实例把已开的窗口叫到最前，自己退出。
let instanceLock = InstanceLock()
guard instanceLock.acquire() else {
    InstanceLock.signalExisting()
    exit(0)
}

let appDelegate = AppDelegate(instanceLock: instanceLock)
let application = NSApplication.shared
application.delegate = appDelegate
application.setActivationPolicy(.regular)
application.run()
