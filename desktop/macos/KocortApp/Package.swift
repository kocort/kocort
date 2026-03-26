// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "KocortApp",
    platforms: [
        .macOS(.v13)
    ],
    targets: [
        .executableTarget(
            name: "KocortApp",
            path: "Sources",
            resources: []
        )
    ]
)
