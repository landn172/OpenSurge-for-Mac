import AppKit
import Foundation
import ServiceManagement

@MainActor
final class StatusModel: ObservableObject {
    @Published private(set) var status: MenuBarStatus?
    @Published private(set) var error: String?
    @Published private(set) var isRefreshing = false
    @Published private(set) var serviceNeedsReconnect = false
    @Published private(set) var isChangingServices = false
    @Published private(set) var isUninstalling = false
    @Published private(set) var isCheckingForUpdate = false
    @Published private(set) var availableUpdate: AvailableUpdate?
    @Published private(set) var updateCheckMessage: String?
    @Published private(set) var pendingGatewayAction: GatewayAction?
    @Published var openAtLogin = false
    @Published var autoStartGateway: Bool {
        didSet { defaults.set(autoStartGateway, forKey: Self.autoStartDefaultsKey) }
    }

    static let autoStartDefaultsKey = "OpenSurgeAutoStartGateway"

    private let client: ControlAPIClient
    private let urlLauncher: WebGUIURLLauncher
    private let updateChecker: UpdateChecker
    let currentVersion: String
    private var timer: Timer?
    private var rapidPolling = false
    private var failureCount = 0
    private var isQuitting = false
    private var lastAutomaticUpdateCheck: Date?
    private var autoStartResolved = false
    private let defaults: UserDefaults
    /// The server gives a gateway operation three minutes; the menu bar stops
    /// waiting a little earlier and points at the panel rather than hang.
    private static let gatewayOperationTimeout: TimeInterval = 170

    init(
        client: ControlAPIClient = ControlAPIClient(),
        urlLauncher: WebGUIURLLauncher = WebGUIURLLauncher(),
        updateChecker: UpdateChecker = UpdateChecker(),
        defaults: UserDefaults = .standard,
        currentVersion: String = installedReleaseVersion(
            releaseTag: Bundle.main.object(forInfoDictionaryKey: "OpenSurgeReleaseTag") as? String,
            shortVersion: Bundle.main.object(
                forInfoDictionaryKey: "CFBundleShortVersionString"
            ) as? String
        )
    ) {
        self.client = client
        self.urlLauncher = urlLauncher
        self.updateChecker = updateChecker
        self.currentVersion = currentVersion
        self.defaults = defaults
        // Auto start is the default: opening OpenSurge is what a user does when
        // they want the gateway up. Every guard that makes it safe lives in
        // `menuBarAutoStartDecision`, not in this default.
        self.autoStartGateway = defaults.object(forKey: Self.autoStartDefaultsKey) as? Bool ?? true
        self.openAtLogin = SMAppService.mainApp.status == .enabled
    }

    var indicator: IndicatorState { menuBarIndicator(status: status, hasError: error != nil) }
    var canQuitOpenSurge: Bool { status?.canQuitOpenSurge == true && !isChangingServices }
    var canUninstall: Bool { status?.canUninstall == true && !isChangingServices }

    func startPolling(rapid: Bool = false) {
        guard !isQuitting else { return }
        timer?.invalidate()
        rapidPolling = rapid
        Task { await refresh() }
        checkForUpdatesAutomaticallyIfNeeded()
    }

    func stopRapidPolling() {
        guard !isQuitting else { return }
        startPolling(rapid: false)
    }

    func refresh() async {
        guard !isQuitting, !isRefreshing else { return }
        timer?.invalidate()
        isRefreshing = true
        defer { isRefreshing = false; scheduleNextRefresh() }
        do {
            recordStatus(try await client.status())
        } catch let controlError as ControlAPIError where controlError.serviceUnavailable {
            guard !isQuitting else { return }
            await ControlServiceLauncher.wake()
            guard !isQuitting else { return }
            try? await Task.sleep(for: .milliseconds(350))
            guard !isQuitting else { return }
            do {
                recordStatus(try await client.status())
            } catch {
                recordFailure(error)
            }
        } catch {
            recordFailure(error)
        }
    }

    private func recordStatus(_ value: MenuBarStatus) {
        status = value
        error = nil
        serviceNeedsReconnect = false
        failureCount = 0
        autoStartGatewayIfNeeded()
    }

    func reconnectService() async {
        guard !isQuitting, !isRefreshing else { return }
        timer?.invalidate()
        isRefreshing = true
        error = nil
        serviceNeedsReconnect = false
        await ControlServiceLauncher.wake(restart: true)
        try? await Task.sleep(for: .milliseconds(350))
        isRefreshing = false
        await refresh()
    }

    var gatewaySwitch: GatewaySwitchState {
        menuBarGatewaySwitch(status: status, pendingAction: pendingGatewayAction)
    }

    func setGatewayRunning(_ running: Bool) {
        performGatewayAction(running ? .start : .stop)
    }

    /// Stopping an interrupted runtime is a reconciliation, not an ordinary
    /// stop: it removes state left by the previous boot without signalling
    /// this boot's processes or touching its PF and forwarding.
    func cleanupInterruptedRuntime() {
        performGatewayAction(.stop)
    }

    private func performGatewayAction(_ action: GatewayAction) {
        guard !isQuitting, pendingGatewayAction == nil else { return }
        pendingGatewayAction = action
        error = nil
        Task {
            let failure = await runGatewayAction(action)
            pendingGatewayAction = nil
            if let failure { error = failure }
            await refreshAfterGatewayAction()
        }
    }

    private func runGatewayAction(_ action: GatewayAction) async -> String? {
        do {
            var operation = try await client.gateway(action)
            let deadline = Date().addingTimeInterval(Self.gatewayOperationTimeout)
            while !operation.isFinished {
                guard Date() < deadline else {
                    return "网关\(action.verb)尚未返回结果，请在面板的网络设置中确认状态"
                }
                try await Task.sleep(for: .milliseconds(500))
                guard !isQuitting else { return nil }
                operation = try await client.operation(id: operation.id)
            }
            guard operation.failed else { return nil }
            return operation.error.map { "网关\(action.verb)失败：\($0)" } ?? "网关\(action.verb)失败"
        } catch {
            return error.localizedDescription
        }
    }

    // `refresh()` no-ops while another refresh is in flight, and that one may
    // have read status before the operation finished. Wait for it to land so the
    // switch settles on the post-action state instead of on a stale sample.
    private func refreshAfterGatewayAction() async {
        var waited = 0
        while isRefreshing, waited < 20 {
            try? await Task.sleep(for: .milliseconds(150))
            waited += 1
        }
        await refresh()
    }

    private func autoStartGatewayIfNeeded() {
        let decision = menuBarAutoStartDecision(
            status: status,
            enabled: autoStartGateway,
            alreadyAttempted: autoStartResolved
        )
        // The first status of this launch decides, once — including when it
        // decides not to start. Anything after it is the user's own doing, and a
        // gateway they just switched off must not come back on the next poll.
        autoStartResolved = true
        guard case .start = decision else { return }
        performGatewayAction(.start)
    }

    func quitMenuBarApp() -> Never {
        isQuitting = true
        timer?.invalidate()
        ControlServiceLauncher.terminateMenuBarApp()
    }

    func quitOpenSurge() {
        guard canQuitOpenSurge else {
            error = openSurgeQuitWarning(for: status)
            return
        }
        timer?.invalidate()
        isQuitting = true
        isChangingServices = true
        error = nil
        Task {
            do {
                try await ControlServiceLauncher.stopControlService()
                ControlServiceLauncher.terminateMenuBarApp()
            } catch {
                isQuitting = false
                self.error = error.localizedDescription
                isChangingServices = false
                scheduleNextRefresh()
            }
        }
    }

    func uninstall(_ mode: UninstallMode) {
        guard canUninstall else {
            error = uninstallWarning(for: status)
            return
        }
        timer?.invalidate()
        isQuitting = true
        isChangingServices = true
        isUninstalling = true
        error = nil
        let restoreLoginItemOnFailure = openAtLogin
        if restoreLoginItemOnFailure {
            try? SMAppService.mainApp.unregister()
            openAtLogin = false
        }
        Task {
            do {
                try await OpenSurgeUninstaller.run(mode: mode)
                ControlServiceLauncher.terminateMenuBarApp()
            } catch OpenSurgeUninstallError.authorizationCancelled {
                restoreAfterUninstallFailure(
                    error: nil,
                    restoreLoginItem: restoreLoginItemOnFailure
                )
            } catch {
                restoreAfterUninstallFailure(
                    error: error.localizedDescription,
                    restoreLoginItem: restoreLoginItemOnFailure
                )
            }
        }
    }

    private func restoreAfterUninstallFailure(error: String?, restoreLoginItem: Bool) {
        if restoreLoginItem {
            try? SMAppService.mainApp.register()
            openAtLogin = true
        }
        isQuitting = false
        isChangingServices = false
        isUninstalling = false
        self.error = error
        scheduleNextRefresh()
    }

    private func recordFailure(_ error: Error) {
        guard !isQuitting else { return }
        status = nil
        self.error = error.localizedDescription
        serviceNeedsReconnect = (error as? ControlAPIError)?.serviceUnavailable ?? false
        failureCount += 1
    }

    private func scheduleNextRefresh() {
        guard !isQuitting else { return }
        let base = rapidPolling ? 2.0 : 15.0
        let multiplier = pow(2.0, Double(min(failureCount, 4)))
        let interval = min(base * multiplier, 60.0)
        timer = Timer.scheduledTimer(withTimeInterval: interval, repeats: false) { [weak self] _ in
            guard let model = self else { return }
            Task { @MainActor in await model.refresh() }
        }
    }

    func openWebGUI(path: String = "dashboard") async {
        do {
            let url = try await client.bootstrapURL(path: path)
            try urlLauncher.open(url)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func copyDiagnostics() {
        let text = status?.diagnosticSummary ?? "OpenSurge Control API: \(error ?? "unreachable")"
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    func checkForUpdates(manual: Bool = true) async {
        guard !isQuitting, !isCheckingForUpdate else { return }
        isCheckingForUpdate = true
        if manual { updateCheckMessage = nil }
        defer { isCheckingForUpdate = false }

        do {
            availableUpdate = try await updateChecker.check(currentVersion: currentVersion)
            updateCheckMessage = availableUpdate == nil ? "当前已是最新稳定版" : nil
        } catch {
            updateCheckMessage = manual
                ? "检查更新失败：\(error.localizedDescription)"
                : "自动检查更新失败，可手动重试"
        }
    }

    func openUpdateDownloadPage() {
        guard let availableUpdate else { return }
        do {
            try urlLauncher.open(availableUpdate.releasePage)
        } catch {
            updateCheckMessage = "无法打开下载页，请前往 OpenSurge GitHub Releases"
        }
    }

    private func checkForUpdatesAutomaticallyIfNeeded() {
        let now = Date()
        if let lastAutomaticUpdateCheck,
           now.timeIntervalSince(lastAutomaticUpdateCheck) < 24 * 60 * 60 {
            return
        }
        lastAutomaticUpdateCheck = now
        Task { await checkForUpdates(manual: false) }
    }
}
