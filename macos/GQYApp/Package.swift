// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "GQYApp",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "GQYApp", path: "Sources/GQYApp")
    ]
)
