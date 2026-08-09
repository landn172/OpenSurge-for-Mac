import AppKit
import Foundation
import Darwin

enum CheckFailure: Error, CustomStringConvertible {
    case failed(String)
    var description: String { if case .failed(let message) = self { return message }; return "check failed" }
}

func require(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    if !condition() { throw CheckFailure.failed(message) }
}

private final class CheckURLProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (HTTPURLResponse, Data))?
    nonisolated(unsafe) static var lastFailure: String?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            guard let handler = Self.handler else { throw CheckFailure.failed("missing URLProtocol handler") }
            let (response, data) = try handler(request)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch { Self.lastFailure = String(describing: error); client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}
}

@main
struct MenuBarChecks {
    static func main() async {
        do { try await run() }
        catch { fputs("OpenSurge menu bar checks failed: \(error)\n", stderr); exit(1) }
    }

    static func run() async throws {
        let directory = FileManager.default.temporaryDirectory.appending(path: "opensurge-menubar-check-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        try Data(#"{"schema_version":1,"url":"http://127.0.0.1:61767"}"#.utf8).write(to: directory.appending(path: "control-endpoint.json"))
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CheckURLProtocol.self]
        let client = ControlAPIClient(session: URLSession(configuration: configuration), applicationSupport: directory, tokenOverride: "test-token")

        let updateChecker = UpdateChecker(session: URLSession(configuration: configuration))
        try require(
            installedReleaseVersion(releaseTag: "v0.1.24-rc.1", shortVersion: "0.1.24") == "0.1.24-rc.1",
            "the full packaged release tag must take precedence over the numeric bundle version"
        )
        try require(
            installedReleaseVersion(releaseTag: nil, shortVersion: "0.1.23") == "0.1.23",
            "older packages without a full release tag must retain numeric-version fallback"
        )
        CheckURLProtocol.handler = { request in
            try require(request.url?.absoluteString == "https://api.github.com/repos/YTwsy/OpenSurge-for-Mac/releases/latest", "latest release path mismatch")
            try require(request.value(forHTTPHeaderField: "Accept") == "application/vnd.github+json", "GitHub API accept header missing")
            try require(request.value(forHTTPHeaderField: "User-Agent") == "OpenSurge-for-Mac/0.1.23", "update User-Agent mismatch")
            let body = #"{"tag_name":"v0.1.24","html_url":"https://github.com/YTwsy/OpenSurge-for-Mac/releases/tag/v0.1.24","draft":false,"prerelease":false}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let availableUpdate: AvailableUpdate?
        do {
            availableUpdate = try await updateChecker.check(currentVersion: "0.1.23")
        } catch {
            throw CheckFailure.failed("update request failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))")
        }
        try require(availableUpdate?.version == "0.1.24", "new stable version was not discovered")
        try require(availableUpdate?.releasePage.path.hasSuffix("/releases/tag/v0.1.24") == true, "update did not retain the version-specific download page")
        CheckURLProtocol.handler = { request in
            try require(request.value(forHTTPHeaderField: "User-Agent") == "OpenSurge-for-Mac/0.1.24-rc.1", "release-candidate User-Agent mismatch")
            let body = #"{"tag_name":"v0.1.24","html_url":"https://github.com/YTwsy/OpenSurge-for-Mac/releases/tag/v0.1.24","draft":false,"prerelease":false}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let releaseCandidateUpdate = try await updateChecker.check(currentVersion: "0.1.24-rc.1")
        try require(
            releaseCandidateUpdate?.version == "0.1.24",
            "the stable release must supersede the same-version release candidate"
        )
        CheckURLProtocol.handler = { request in
            try require(request.value(forHTTPHeaderField: "User-Agent") == "OpenSurge-for-Mac/0.1.24", "current-version User-Agent mismatch")
            let body = #"{"tag_name":"v0.1.24","html_url":"https://github.com/YTwsy/OpenSurge-for-Mac/releases/tag/v0.1.24","draft":false,"prerelease":false}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let alreadyCurrent: AvailableUpdate?
        do {
            alreadyCurrent = try await updateChecker.check(currentVersion: "0.1.24")
        } catch {
            throw CheckFailure.failed("current-version request failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))")
        }
        try require(alreadyCurrent == nil, "current stable version must not be offered again")

        CheckURLProtocol.handler = { request in
            try require(request.value(forHTTPHeaderField: "User-Agent") == "OpenSurge-for-Mac/0.1.25-rc.1", "newer release-candidate User-Agent mismatch")
            let body = #"{"tag_name":"v0.1.24","html_url":"https://github.com/YTwsy/OpenSurge-for-Mac/releases/tag/v0.1.24","draft":false,"prerelease":false}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let olderStable = try await updateChecker.check(currentVersion: "0.1.25-rc.1")
        try require(olderStable == nil, "a release candidate must not be downgraded to an older stable release")

        CheckURLProtocol.handler = { request in
            let body = #"{"tag_name":"v0.1.25","html_url":"https://example.com/untrusted","draft":false,"prerelease":false}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        do {
            _ = try await updateChecker.check(currentVersion: "0.1.24")
            throw CheckFailure.failed("untrusted release page was accepted")
        } catch UpdateCheckError.invalidRelease {
            // Expected: the update button can only open this repository's GitHub release page.
        }

        CheckURLProtocol.handler = { _ in throw URLError(.notConnectedToInternet) }
        do {
            _ = try await updateChecker.check(currentVersion: "0.1.24")
            throw CheckFailure.failed("update transport failure did not fail")
        } catch UpdateCheckError.networkUnavailable {
            // Expected: update connectivity stays separate from Control Service state.
        }

        CheckURLProtocol.handler = { request in
            try require(request.value(forHTTPHeaderField: "Authorization") == "Bearer test-token", "status bearer token missing")
            try require(request.url?.path == "/api/v1/menubar", "status path mismatch")
            let body = #"{"schema_version":1,"revision":"r1","gateway":"running","topology":"same_wifi_dhcp","lan_ip":"192.168.1.20","dhcp":"running","mihomo":"running","pf_anchor":"loaded","forwarding":"enabled","client_count":2,"drift":false,"doctor_healthy":true,"recovery_required":false,"warnings":[]}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let status: MenuBarStatus
        do { status = try await client.status() }
        catch { throw CheckFailure.failed("status request failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))") }
        try require(status.indicator == .running, "running indicator mismatch")
        try require(menuBarIndicator(status: nil, hasError: false) == .connecting, "initial menu bar state must use the brand icon")
        try require(menuBarIndicator(status: nil, hasError: true) == .unreachable, "confirmed Control Service failure must remain distinguishable")
        try require(IndicatorState.connecting.usesBrandMenuBarIcon && IndicatorState.connecting.menuBarIconOpacity == 0.75, "connecting indicator must use the brand icon")
        try require(IndicatorState.unreachable.usesBrandMenuBarIcon && IndicatorState.unreachable.menuBarIconOpacity == 0.35, "initial Control Service delay must not restore the legacy-looking icon")
        try require(status.topologyLabel == "局域网 DHCP 接管", "DHCP takeover topology label mismatch")
        try require(status.gatewayServicesActive && menuBarQuitWarning(for: status).contains("都不会停止"), "active gateway quit warning mismatch")
        try require(status.diagnosticSummary.contains("PF: loaded"), "diagnostic summary omitted PF")

        try Data("file-token".utf8).write(to: directory.appending(path: "control-token"))
        let fileTokenClient = ControlAPIClient(session: URLSession(configuration: configuration), applicationSupport: directory)
        CheckURLProtocol.handler = { request in
            try require(request.value(forHTTPHeaderField: "Authorization") == "Bearer file-token", "application support token was not used")
            let body = #"{"schema_version":1,"revision":"r1","gateway":"stopped","topology":"isolated_lan","lan_ip":"192.168.50.1","dhcp":"stopped","mihomo":"stopped","pf_anchor":"unloaded","forwarding":"disabled","client_count":0,"drift":false,"doctor_healthy":true,"recovery_required":false,"warnings":[]}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        _ = try await fileTokenClient.status()

        CheckURLProtocol.handler = { _ in throw URLError(.cannotConnectToHost) }
        do {
            _ = try await client.status()
            throw CheckFailure.failed("transport failure did not fail")
        } catch let error as ControlAPIError {
            try require(error.serviceUnavailable, "transport failure did not trigger Control Service recovery")
        }

        try FileManager.default.removeItem(at: directory.appending(path: "control-token"))
        do {
            _ = try await fileTokenClient.status()
            throw CheckFailure.failed("missing local token did not fail")
        } catch let error as ControlAPIError {
            try require(error.serviceUnavailable, "missing local token was not classified as a service availability failure")
            try require(error.localizedDescription == "OpenSurge 后台服务尚未准备好", "missing local token exposed a technical error")
        }

        CheckURLProtocol.handler = { request in
            try require(request.url?.query == nil, "long-lived token leaked into request URL")
            try require(request.value(forHTTPHeaderField: "Authorization") == "Bearer test-token", "bootstrap bearer token missing")
            try require(String(decoding: requestBody(request), as: UTF8.self) == #"{"path":"network"}"#, "bootstrap deep-link body mismatch")
            let body = #"{"schema_version":1,"url":"http://127.0.0.1:61767/bootstrap?code=one-time","expires_at":"2026-07-12T00:00:00.123456789Z"}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 201, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let bootstrap: URL
        do { bootstrap = try await client.bootstrapURL(path: "network") }
        catch { throw CheckFailure.failed("bootstrap request failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))") }
        try require(bootstrap.query == "code=one-time" && !bootstrap.absoluteString.contains("test-token"), "bootstrap URL leaked long-lived token")

        let active = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "running", topology: "same_wifi_dhcp", lanIp: "192.168.1.20", dhcp: "running", mihomo: "running", pfAnchor: "loaded", forwarding: "enabled", clientCount: 2, drift: false, doctorHealthy: true, recoveryRequired: true, recoveryStage: "gateway_active", warnings: [], errorCode: nil)
        try require(active.takeoverActive && !active.recoveryNeedsAttention && active.indicator == .running, "active takeover must use the running indicator")
        let recovery = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "stopped", topology: "same_wifi_dhcp", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "disabled", clientCount: 2, drift: true, doctorHealthy: false, recoveryRequired: true, recoveryStage: "gateway_stopped_waiting_router_dhcp", warnings: [], errorCode: nil)
        try require(recovery.recoveryNeedsAttention && recovery.indicator == .recovery && recovery.indicator.systemImage == "exclamationmark.triangle.fill", "post-stop recovery must have highest priority")
        let prepared = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "stopped", topology: "same_wifi_dhcp", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "disabled", clientCount: 0, drift: false, doctorHealthy: true, recoveryRequired: true, recoveryStage: "prepared", warnings: [], errorCode: nil)
        try require(prepared.recoverySnapshotPrepared && !prepared.recoveryNeedsAttention && prepared.indicator == .stopped, "prepared recovery must not present as a network recovery")
        let stopped = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "stopped", topology: "same_wifi_dhcp", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "disabled", clientCount: 0, drift: true, doctorHealthy: false, recoveryRequired: false, recoveryStage: nil, warnings: [], errorCode: nil)
        try require(stopped.indicator == .stopped && stopped.indicator.accessibilityLabel == "OpenSurge 网关已停止", "stopped gateway must not be presented as a runtime failure")
        try require(stopped.canQuitOpenSurge && openSurgeQuitWarning(for: stopped).contains("root Helper 仍保持空闲加载"), "stopped gateway must allow the explicit OpenSurge quit path")
        try require(!active.canQuitOpenSurge && !recovery.canQuitOpenSurge, "active or recovery state must block the OpenSurge quit path")
        try require(!active.canUninstall && recovery.canUninstall, "uninstall must depend only on whether the gateway is stopped")
        let forwardingAlreadyEnabled = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "stopped", topology: "isolated_lan", lanIp: "192.168.50.1", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "enabled", clientCount: 0, drift: false, doctorHealthy: true, recoveryRequired: false, recoveryStage: nil, warnings: [], errorCode: nil)
        try require(!forwardingAlreadyEnabled.gatewayServicesActive && forwardingAlreadyEnabled.canQuitOpenSurge && forwardingAlreadyEnabled.canUninstall, "host forwarding must not block quit or uninstall")

        // MARK: gateway switch and launch-time auto start

        let bypassStopped = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "stopped", runtimeState: "none", topology: "same_lan", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "disabled", clientCount: 0, drift: true, doctorHealthy: true, recoveryRequired: false, recoveryStage: nil, warnings: [], errorCode: nil)
        let bypassRunning = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "running", runtimeState: "active", topology: "same_lan", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "running", pfAnchor: "loaded", forwarding: "enabled", clientCount: 3, drift: false, doctorHealthy: true, recoveryRequired: false, recoveryStage: nil, warnings: [], errorCode: nil)
        let bypassInterrupted = MenuBarStatus(schemaVersion: 1, revision: "r", gateway: "degraded", runtimeState: "interrupted", topology: "same_lan", lanIp: "192.168.1.20", dhcp: "stopped", mihomo: "stopped", pfAnchor: "unloaded", forwarding: "unknown", clientCount: 0, drift: false, doctorHealthy: false, recoveryRequired: false, recoveryStage: nil, warnings: [], errorCode: nil)

        try require(
            menuBarGatewaySwitch(status: bypassStopped, pendingAction: nil) == GatewaySwitchState(isOn: false, isEnabled: true, subtitle: "已停止", help: "打开后会按当前配置启动网关"),
            "a stopped bypass gateway must offer the switch, and unapplied config drift must not block starting it"
        )
        try require(
            menuBarGatewaySwitch(status: bypassRunning, pendingAction: nil).isOn
                && menuBarGatewaySwitch(status: bypassRunning, pendingAction: nil).isEnabled,
            "a running bypass gateway must be stoppable from the switch"
        )
        try require(
            menuBarGatewaySwitch(status: bypassStopped, pendingAction: .start) == GatewaySwitchState(isOn: true, isEnabled: false, subtitle: "正在启动…", help: "正在启动网关，完成前无法再次切换"),
            "an in-flight start must hold the switch at its requested state without a spinner"
        )
        try require(
            menuBarGatewaySwitch(status: nil, pendingAction: nil).isEnabled == false,
            "the switch must stay inert until the Control Service reports a status"
        )
        try require(
            menuBarGatewaySwitch(status: bypassInterrupted, pendingAction: nil) == GatewaySwitchState(isOn: false, isEnabled: false, subtitle: "重启后待清理", help: "上次开机留下的运行状态需要先安全清理，然后才能重新启动网关"),
            "a runtime left by the previous boot reports gateway=degraded, and the switch must name it instead of offering a start the gateway will refuse"
        )
        try require(
            menuBarGatewaySwitch(status: active, pendingAction: nil).isEnabled == false
                && menuBarGatewaySwitch(status: stopped, pendingAction: nil).isEnabled == false,
            "same-LAN DHCP takeover starts and stops only through the recovery flow, never from the switch"
        )
        try require(
            menuBarGatewaySwitch(status: recovery, pendingAction: nil).isEnabled == false,
            "an unfinished network recovery must block the switch"
        )

        try require(
            menuBarAutoStartDecision(status: bypassStopped, enabled: true, alreadyAttempted: false) == .start,
            "opening the app must start a stopped bypass gateway"
        )
        try require(
            menuBarAutoStartDecision(status: bypassStopped, enabled: false, alreadyAttempted: false) != .start,
            "auto start must honour the preference"
        )
        try require(
            menuBarAutoStartDecision(status: bypassStopped, enabled: true, alreadyAttempted: true) != .start,
            "auto start must run at most once per launch, so a failed start is not retried on every poll"
        )
        try require(
            menuBarAutoStartDecision(status: nil, enabled: true, alreadyAttempted: false) != .start
                && menuBarAutoStartDecision(status: bypassRunning, enabled: true, alreadyAttempted: false) != .start
                && menuBarAutoStartDecision(status: bypassInterrupted, enabled: true, alreadyAttempted: false) != .start,
            "auto start must require a known, fully stopped gateway"
        )
        try require(
            menuBarAutoStartDecision(status: stopped, enabled: true, alreadyAttempted: false) != .start,
            "auto start must never drive same-LAN DHCP takeover: the Control API accepts it only after router DHCP was confirmed disabled"
        )
        try require(
            menuBarAutoStartDecision(status: recovery, enabled: true, alreadyAttempted: false) != .start,
            "auto start must never run while network recovery is unfinished"
        )

        var gatewayRequests: [(path: String, method: String, idempotencyKey: String?)] = []
        CheckURLProtocol.handler = { request in
            gatewayRequests.append((request.url?.path ?? "", request.httpMethod ?? "", request.value(forHTTPHeaderField: "Idempotency-Key")))
            try require(request.value(forHTTPHeaderField: "Authorization") == "Bearer test-token", "gateway action bearer token missing")
            let body = #"{"schema_version":1,"id":"op-1","kind":"start","state":"running","created_at":"2026-07-12T00:00:00Z","updated_at":"2026-07-12T00:00:00Z"}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 202, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let firstStart: GatewayOperation
        let secondStart: GatewayOperation
        do {
            firstStart = try await client.gateway(.start)
            secondStart = try await client.gateway(.start)
        } catch { throw CheckFailure.failed("gateway start request failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))") }
        try require(gatewayRequests.count == 2 && gatewayRequests.allSatisfy { $0.path == "/api/v1/gateway/start" && $0.method == "POST" }, "gateway start must POST the Control API start action")
        try require(gatewayRequests.allSatisfy { $0.idempotencyKey?.isEmpty == false }, "gateway actions must carry an Idempotency-Key")
        try require(
            gatewayRequests[0].idempotencyKey != gatewayRequests[1].idempotencyKey,
            "each attempt needs a fresh key: the Control API stores this header as the operation id and replays the stored operation for a repeat"
        )
        try require(!firstStart.isFinished && firstStart.id == "op-1" && secondStart.id == "op-1", "a 202 operation is not a result; the switch must keep polling it")

        CheckURLProtocol.handler = { request in
            try require(request.url?.path == "/api/v1/operations/op-1" && request.httpMethod == "GET", "operation polling path mismatch")
            let body = #"{"schema_version":1,"id":"op-1","kind":"start","state":"failed","error":"preflight failed","created_at":"2026-07-12T00:00:00Z","updated_at":"2026-07-12T00:00:05Z"}"#
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
        }
        let finishedStart: GatewayOperation
        do { finishedStart = try await client.operation(id: "op-1") }
        catch { throw CheckFailure.failed("operation poll failed: \(CheckURLProtocol.lastFailure ?? String(describing: error))") }
        try require(finishedStart.isFinished && finishedStart.failed && finishedStart.error == "preflight failed", "a failed gateway operation must surface its reason instead of reading as success")

        try require(
            gatewayStopConfirmation(clientCount: 3).message.contains("3 台"),
            "stopping while downstream clients are served must name how many lose service"
        )

        let stoppedBypassBody = #"{"schema_version":1,"revision":"r","gateway":"stopped","runtime_state":"none","topology":"same_lan","lan_ip":"192.168.1.20","dhcp":"stopped","mihomo":"stopped","tun":"stopped","pf_anchor":"unloaded","forwarding":"disabled","client_count":0,"drift":false,"doctor_healthy":true,"recovery_required":false,"warnings":[]}"#
        var autoStartCount = 0
        CheckURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            switch path {
            case "/api/v1/menubar":
                return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(stoppedBypassBody.utf8))
            case "/api/v1/gateway/start":
                autoStartCount += 1
                let body = #"{"schema_version":1,"id":"auto-start","kind":"start","state":"succeeded","created_at":"2026-07-12T00:00:00Z","updated_at":"2026-07-12T00:00:09Z"}"#
                return (HTTPURLResponse(url: request.url!, statusCode: 202, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
            default:
                throw CheckFailure.failed("unexpected request during auto start: \(path)")
            }
        }
        let autoStartModel = await StatusModel(
            client: client,
            defaults: UserDefaults(suiteName: "opensurge-menubar-check-\(UUID().uuidString)")!,
            currentVersion: "0.1.24"
        )
        await autoStartModel.refresh()
        try await settle(autoStartModel)
        try require(autoStartCount == 1, "opening the app must start a stopped bypass gateway")
        await autoStartModel.refresh()
        try await settle(autoStartModel)
        try require(autoStartCount == 1, "auto start must run at most once per launch")

        // Launch finds the gateway already running, so auto start declines. When
        // the user later switches it off, that decision must stick: auto start
        // belongs to the launch, and a poll is not a new launch.
        let runningBypassBody = #"{"schema_version":1,"revision":"r","gateway":"running","runtime_state":"active","topology":"same_lan","lan_ip":"192.168.1.20","dhcp":"stopped","mihomo":"running","tun":"ready","pf_anchor":"loaded","forwarding":"enabled","client_count":2,"drift":false,"doctor_healthy":true,"recovery_required":false,"warnings":[]}"#
        var statusResponses = 0
        var startsAfterManualStop = 0
        CheckURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            switch path {
            case "/api/v1/menubar":
                statusResponses += 1
                let body = statusResponses == 1 ? runningBypassBody : stoppedBypassBody
                return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
            case "/api/v1/gateway/start":
                startsAfterManualStop += 1
                let body = #"{"schema_version":1,"id":"unwanted-start","kind":"start","state":"succeeded","created_at":"2026-07-12T00:00:00Z","updated_at":"2026-07-12T00:00:09Z"}"#
                return (HTTPURLResponse(url: request.url!, statusCode: 202, httpVersion: nil, headerFields: nil)!, Data(body.utf8))
            default:
                throw CheckFailure.failed("unexpected request after manual stop: \(path)")
            }
        }
        let manualStopModel = await StatusModel(
            client: client,
            defaults: UserDefaults(suiteName: "opensurge-menubar-check-\(UUID().uuidString)")!,
            currentVersion: "0.1.24"
        )
        await manualStopModel.refresh()
        try await settle(manualStopModel)
        await manualStopModel.refresh()
        try await settle(manualStopModel)
        try require(startsAfterManualStop == 0, "auto start must not undo a gateway the user switched off after launch")

        let (useDefaultReopen, presentationCount) = await MainActor.run {
            let panelPresenter = CheckMenuBarPresenter()
            let appDelegate = OpenSurgeAppDelegate(presenter: panelPresenter)
            let useDefaultReopen = appDelegate.applicationShouldHandleReopen(NSApplication.shared, hasVisibleWindows: false)
            return (useDefaultReopen, panelPresenter.presentationCount)
        }
        try require(!useDefaultReopen && presentationCount == 1, "opening OpenSurge again must show the menu bar panel")

        let launchPresentationCount = await presentationCountAfterLaunch()
        try require(launchPresentationCount == 1, "launching OpenSurge must show the menu bar panel")

        let applicationStateChangeCount = await stateChangeCountAfterLifecycleEvents()
        try require(
            applicationStateChangeCount == 1,
            "AppKit activation must enhance a visible panel without update-event churn"
        )
        try require(
            panelPresentationAction(anchorVisible: false) == .waitForAnchor,
            "panel presentation must wait for a visible status-item anchor"
        )
        try require(
            panelPresentationAction(popoverShown: false) == .showPopover,
            "ready panel presentation must show the popover"
        )
        try require(
            panelPresentationAction(
                panelWindowAvailable: false,
                popoverWindowWaitExpired: false
            ) == .waitForPanelWindow,
            "popover animation must receive a grace interval before recovery"
        )
        try require(
            panelPresentationAction(
                panelWindowAvailable: false,
                popoverWindowWaitExpired: true
            ) == .replacePopover,
            "a logical popover still missing its window after the grace interval must be replaced"
        )
        try require(
            panelPresentationAction() == .complete,
            "a real popover window must complete presentation without waiting for activation or key status"
        )
        try require(
            menuBarPanelPresentationAction(
                presentationPending: false,
                anchorVisible: true,
                popoverShown: true,
                panelWindowAvailable: false,
                popoverWindowWaitExpired: true
            ) == .none,
            "a completed or abandoned presentation must not keep advancing"
        )
        try require(
            MenuBarPanelRetryPolicy.delay(after: 0) == 0.05
                && MenuBarPanelRetryPolicy.delay(after: 5) == 0.1
                && MenuBarPanelRetryPolicy.delay(after: 10) == 0.25,
            "panel presentation retry policy must back off instead of spinning"
        )
        try require(
            MenuBarPanelRetryPolicy.presentationWindow
                > MenuBarPanelRetryPolicy.popoverWindowGrace,
            "the bounded startup retry window must allow at least one failed popover recovery"
        )
        try require(
            menuBarPopoverBehavior(applicationActive: false) == .applicationDefined
                && menuBarPopoverBehavior(applicationActive: true) == .applicationDefined,
            "the panel must own popover dismissal in both activation states, so a click in an AppKit submenu window cannot be mistaken for a click outside the panel"
        )
        try require(
            !menuBarStatusItemNeedsRefresh(
                renderedIndicator: .running,
                nextIndicator: .running
            ) && menuBarStatusItemNeedsRefresh(
                renderedIndicator: .running,
                nextIndicator: .degraded
            ),
            "status polling must redraw the menu bar icon only when its indicator changes"
        )

        var fallbackOpened = false
        let launcher = WebGUIURLLauncher(
            workspaceOpen: { _ in false },
            commandOpen: { _ in fallbackOpened = true }
        )
        try launcher.open(URL(string: "http://127.0.0.1:61767/bootstrap?code=test")!)
        try require(fallbackOpened, "workspace URL failure did not use the open command fallback")

        let failingLauncher = WebGUIURLLauncher(
            workspaceOpen: { _ in false },
            commandOpen: { _ in throw CheckFailure.failed("simulated browser failure") }
        )
        do {
            try failingLauncher.open(URL(string: "http://127.0.0.1:61767/bootstrap?code=test")!)
            throw CheckFailure.failed("browser failure was silently ignored")
        } catch WebGUIURLLaunchError.browserUnavailable {
            // Expected: the caller can surface this without leaking the bootstrap URL.
        }

        print("OpenSurge menu bar checks passed")
    }
}

/// Waits for the model to have no gateway operation and no refresh in flight,
/// so the next assertion reads settled state rather than a mid-flight sample.
private func settle(_ model: StatusModel) async throws {
    for _ in 0..<200 {
        let busy = await MainActor.run { model.pendingGatewayAction != nil || model.isRefreshing }
        if !busy { return }
        try await Task.sleep(for: .milliseconds(20))
    }
    throw CheckFailure.failed("gateway action did not settle")
}

@MainActor
private func presentationCountAfterLaunch() async -> Int {
    let panelPresenter = CheckMenuBarPresenter()
    let appDelegate = OpenSurgeAppDelegate(presenter: panelPresenter)
    appDelegate.applicationDidFinishLaunching(
        Notification(name: NSApplication.didFinishLaunchingNotification)
    )
    await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
        DispatchQueue.main.async {
            continuation.resume()
        }
    }
    withExtendedLifetime(appDelegate) {}
    return panelPresenter.presentationCount
}

@MainActor
private func stateChangeCountAfterLifecycleEvents() -> Int {
    let panelPresenter = CheckMenuBarPresenter()
    let appDelegate = OpenSurgeAppDelegate(presenter: panelPresenter)
    appDelegate.applicationDidBecomeActive(
        Notification(name: NSApplication.didBecomeActiveNotification)
    )
    return panelPresenter.stateChangeCount
}

@MainActor
private final class CheckMenuBarPresenter: MenuBarPresenting {
    var presentationCount = 0
    var stateChangeCount = 0
    func showPanel() { presentationCount += 1 }
    func applicationDidBecomeActive() { stateChangeCount += 1 }
}

private func panelPresentationAction(
    anchorVisible: Bool = true,
    popoverShown: Bool = true,
    panelWindowAvailable: Bool = true,
    popoverWindowWaitExpired: Bool = true
) -> MenuBarPanelPresentationAction {
    menuBarPanelPresentationAction(
        presentationPending: true,
        anchorVisible: anchorVisible,
        popoverShown: popoverShown,
        panelWindowAvailable: panelWindowAvailable,
        popoverWindowWaitExpired: popoverWindowWaitExpired
    )
}

private func requestBody(_ request: URLRequest) -> Data {
    if let body = request.httpBody { return body }
    guard let stream = request.httpBodyStream else { return Data() }
    stream.open(); defer { stream.close() }
    var result = Data(), buffer = [UInt8](repeating: 0, count: 4096)
    while stream.hasBytesAvailable {
        let count = stream.read(&buffer, maxLength: buffer.count)
        if count <= 0 { break }
        result.append(buffer, count: count)
    }
    return result
}
