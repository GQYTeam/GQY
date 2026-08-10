import Foundation

struct APIError: Error, LocalizedError {
    let status: Int
    let message: String
    var errorDescription: String? { message }
}

private struct ErrorEnvelope: Codable {
    struct Inner: Codable { let message: String }
    let error: Inner
}

/// 仅保留壳层需要的探活能力；页面交互全部由 WKWebView 里的 WebUI 自己完成
final class APIClient {
    let baseURL: URL
    private let session: URLSession

    init(baseURL: URL) {
        self.baseURL = baseURL
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 5
        session = URLSession(configuration: config)
    }

    func health() async -> Bool {
        var url = baseURL
        url.appendPathComponent("api/health")
        var request = URLRequest(url: url)
        request.timeoutInterval = 3
        guard let (data, response) = try? await session.data(for: request) else { return false }
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else { return false }
        return (try? JSONDecoder().decode(Health.self, from: data)) != nil
    }
}

private struct Health: Codable {
    let status: String
}
