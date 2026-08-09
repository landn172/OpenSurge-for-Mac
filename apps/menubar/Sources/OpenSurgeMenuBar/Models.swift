import Foundation

struct MenuBarStatus: Codable, Equatable {
    let schemaVersion: Int
    let revision: String
    let gateway: String
    let runtimeState: String?
    let topology: String
    let lanIp: String
    let dhcp: String
    let mihomo: String
    let tun: String?
    let tunInterface: String?
    let tunError: String?
    let pfAnchor: String
    let forwarding: String
    let clientCount: Int
    let drift: Bool
    let doctorHealthy: Bool
    let recoveryRequired: Bool
    let recoveryStage: String?
    let warnings: [String]
    let errorCode: String?

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case revision, gateway
        case runtimeState = "runtime_state"
        case topology
        case lanIp = "lan_ip"
        case dhcp, mihomo, tun
        case tunInterface = "tun_interface"
        case tunError = "tun_error"
        case pfAnchor = "pf_anchor"
        case forwarding
        case clientCount = "client_count"
        case drift
        case doctorHealthy = "doctor_healthy"
        case recoveryRequired = "recovery_required"
        case recoveryStage = "recovery_stage"
        case warnings
        case errorCode = "error_code"
    }

    init(
        schemaVersion: Int,
        revision: String,
        gateway: String,
        runtimeState: String? = nil,
        topology: String,
        lanIp: String,
        dhcp: String,
        mihomo: String,
        tun: String? = nil,
        tunInterface: String? = nil,
        tunError: String? = nil,
        pfAnchor: String,
        forwarding: String,
        clientCount: Int,
        drift: Bool,
        doctorHealthy: Bool,
        recoveryRequired: Bool,
        recoveryStage: String?,
        warnings: [String],
        errorCode: String?
    ) {
        self.schemaVersion = schemaVersion
        self.revision = revision
        self.gateway = gateway
        self.runtimeState = runtimeState
        self.topology = topology
        self.lanIp = lanIp
        self.dhcp = dhcp
        self.mihomo = mihomo
        self.tun = tun
        self.tunInterface = tunInterface
        self.tunError = tunError
        self.pfAnchor = pfAnchor
        self.forwarding = forwarding
        self.clientCount = clientCount
        self.drift = drift
        self.doctorHealthy = doctorHealthy
        self.recoveryRequired = recoveryRequired
        self.recoveryStage = recoveryStage
        self.warnings = warnings
        self.errorCode = errorCode
    }
}

enum GatewayAction: String, Equatable {
    case start
    case stop

    var verb: String { self == .start ? "启动" : "停止" }
}

/// The Control API answers `POST /api/v1/gateway/{start,stop}` with 202 and an
/// operation that finishes later, so the switch has to poll this to completion
/// rather than treat the response as the result.
struct GatewayOperation: Codable, Equatable {
    let id: String
    let kind: String
    let state: String
    let error: String?

    var isFinished: Bool { state == "succeeded" || state == "failed" }
    var failed: Bool { state == "failed" }
}

struct BootstrapResponse: Codable {
    let schemaVersion: Int
    let url: URL
    let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case url
        case expiresAt = "expires_at"
    }
}

struct EndpointDescriptor: Codable {
    let schemaVersion: Int
    let url: URL

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case url
    }
}

enum IndicatorState: Equatable {
    case connecting, stopped, running, degraded, recovery, unreachable

    var usesBrandMenuBarIcon: Bool {
        self == .connecting || self == .stopped || self == .running || self == .unreachable
    }

    var menuBarIconOpacity: Double {
        switch self {
        case .connecting: 0.75
        case .stopped: 0.55
        case .unreachable: 0.35
        default: 1
        }
    }

    var systemImage: String {
        switch self {
        case .connecting: "network"
        case .stopped: "network"
        case .running: "network.badge.shield.half.filled"
        case .degraded: "exclamationmark.circle"
        case .recovery: "exclamationmark.triangle.fill"
        case .unreachable: "network.slash"
        }
    }

    var accessibilityLabel: String {
        switch self {
        case .connecting: "正在连接 OpenSurge 控制服务"
        case .stopped: "OpenSurge 网关已停止"
        case .running: "OpenSurge 网关正在运行"
        case .degraded: "OpenSurge 网关运行异常"
        case .recovery: "OpenSurge 网络恢复尚未完成"
        case .unreachable: "无法连接 OpenSurge 控制服务"
        }
    }
}

func menuBarIndicator(status: MenuBarStatus?, hasError: Bool) -> IndicatorState {
    if let status { return status.indicator }
    return hasError ? .unreachable : .connecting
}

extension MenuBarStatus {
    var gatewayServicesActive: Bool {
        gateway == "running" || gateway == "degraded" || dhcp == "running" || mihomo == "running" || pfAnchor == "loaded"
    }

    /// A runtime left behind by the previous boot. The gateway field reports
    /// `degraded` for it exactly as it does for a genuinely failing data plane,
    /// but this one must be cleaned up with stop before start is possible.
    var runtimeInterrupted: Bool { runtimeState == "interrupted" }

    var gatewayActive: Bool {
        !runtimeInterrupted && (gateway == "running" || gateway == "degraded")
    }

    var gatewayStopped: Bool { gateway == "stopped" }

    /// The DHCP takeover topology starts and stops the gateway only as steps of
    /// the recovery state machine — the Control API rejects a bare start unless
    /// router DHCP was confirmed disabled — so the switch never drives it.
    var gatewaySwitchable: Bool { topology != "same_wifi_dhcp" }

    var canQuitOpenSurge: Bool {
        gateway == "stopped" && !gatewayServicesActive && !recoveryNeedsAttention
    }

    var canUninstall: Bool {
        gateway == "stopped"
    }

    var topologyLabel: String {
        switch topology {
        case "same_wifi_dhcp": "局域网 DHCP 接管"
        case "same_lan": "旁路由模式"
        case "isolated_lan": "独立下游 LAN"
        default: topology
        }
    }

    var recoveryNeedsAttention: Bool {
        guard recoveryRequired else { return false }
        guard let stage = recoveryStage else { return true }
        return !["prepared", "gateway_active", "client_validated", "client_validation_skipped"].contains(stage)
    }

    var takeoverActive: Bool {
        guard recoveryRequired, let stage = recoveryStage else { return false }
        return ["gateway_active", "client_validated", "client_validation_skipped"].contains(stage)
    }

    var recoverySnapshotPrepared: Bool {
        recoveryRequired && recoveryStage == "prepared"
    }

    var indicator: IndicatorState {
        if recoveryNeedsAttention { return .recovery }
        // A stopped gateway can legitimately fail runtime-oriented doctor
        // checks and can have unapplied desired config. Neither means a
        // gateway that is not running has suffered a runtime failure.
        if gateway == "stopped" { return .stopped }
        if gateway == "degraded" || drift || !doctorHealthy { return .degraded }
        if gateway == "running" { return .running }
        return .stopped
    }

    var diagnosticSummary: String {
        [
            "OpenSurge for Mac",
            "Gateway: \(gateway)",
            "Topology: \(topologyLabel) [\(topology)]",
            "LAN IP: \(lanIp)",
            "DHCP/DNS: \(dhcp)",
            "mihomo: \(mihomo)",
            "TUN: \(tun ?? "unknown")\(tunInterface.map { " [\($0)]" } ?? "")",
            "PF: \(pfAnchor)",
            "Forwarding: \(forwarding)",
            "Clients: \(clientCount)",
            "Drift: \(drift)",
            "Recovery: \(recoveryRequired ? recoveryStage ?? "required" : "none")",
            "Error code: \(errorCode ?? "none")",
        ].joined(separator: "\n")
    }
}

struct GatewaySwitchState: Equatable {
    var isOn: Bool
    var isEnabled: Bool
    var subtitle: String
    var help: String
}

/// Everything the gateway switch renders, derived in one place so the disabled
/// cases are testable without a running Control Service.
func menuBarGatewaySwitch(
    status: MenuBarStatus?,
    pendingAction: GatewayAction?
) -> GatewaySwitchState {
    guard let status else {
        return GatewaySwitchState(
            isOn: false,
            isEnabled: false,
            subtitle: "等待后台控制服务",
            help: "需要先连接 OpenSurge 后台控制服务，才能启停网关"
        )
    }
    if let pendingAction {
        // The switch shows the requested state while the operation runs, so it
        // does not snap back before the next status poll lands.
        return GatewaySwitchState(
            isOn: pendingAction == .start,
            isEnabled: false,
            subtitle: "正在\(pendingAction.verb)…",
            help: "正在\(pendingAction.verb)网关，完成前无法再次切换"
        )
    }
    if !status.gatewaySwitchable {
        return GatewaySwitchState(
            isOn: status.gatewayActive,
            isEnabled: false,
            subtitle: status.gatewayActive ? "运行中" : "已停止",
            help: "局域网 DHCP 接管只能在面板的网络设置中按恢复流程启停"
        )
    }
    if status.runtimeInterrupted {
        return GatewaySwitchState(
            isOn: false,
            isEnabled: false,
            subtitle: "重启后待清理",
            help: "上次开机留下的运行状态需要先安全清理，然后才能重新启动网关"
        )
    }
    if status.recoveryNeedsAttention {
        return GatewaySwitchState(
            isOn: status.gatewayActive,
            isEnabled: false,
            subtitle: "网络恢复未完成",
            help: "网络恢复尚未完成，请先在面板的网络设置中完成恢复"
        )
    }
    if status.gatewayActive {
        return GatewaySwitchState(
            isOn: true,
            isEnabled: true,
            subtitle: status.gateway == "running" ? "运行中" : "运行异常",
            help: "关闭后会停止 DHCP/DNS、mihomo、PF NAT 与 IPv4 转发"
        )
    }
    if status.gatewayStopped {
        return GatewaySwitchState(
            isOn: false,
            isEnabled: true,
            subtitle: "已停止",
            help: "打开后会按当前配置启动网关"
        )
    }
    return GatewaySwitchState(
        isOn: false,
        isEnabled: false,
        subtitle: "状态未知",
        help: "无法确认网关状态，请在面板的网络设置中查看"
    )
}

enum GatewayAutoStartDecision: Equatable {
    case start
    /// Carries why the launch-time start was declined, so the check binary can
    /// pin each refusal instead of only observing that nothing happened.
    case skip(String)
}

func menuBarAutoStartDecision(
    status: MenuBarStatus?,
    enabled: Bool,
    alreadyAttempted: Bool
) -> GatewayAutoStartDecision {
    guard enabled else { return .skip("auto start is disabled") }
    guard !alreadyAttempted else { return .skip("auto start already ran this launch") }
    guard let status else { return .skip("gateway status is unknown") }
    guard status.gatewaySwitchable else {
        return .skip("same-LAN DHCP takeover starts only through the recovery flow")
    }
    guard !status.runtimeInterrupted else {
        return .skip("an interrupted runtime must be cleaned up first")
    }
    guard !status.recoveryNeedsAttention else {
        return .skip("network recovery is unfinished")
    }
    // Only a fully stopped gateway may be started; degraded means a data plane
    // is still up, and start refuses to run over an existing runtime state.
    guard status.gatewayStopped else { return .skip("gateway is not stopped") }
    return .start
}

func menuBarQuitWarning(for status: MenuBarStatus?) -> String {
    guard let status else {
        return "退出只会关闭菜单栏图标；OpenSurge 后台控制服务仍会继续运行。当前无法确认网关服务状态，请先在 Web GUI 或活动监视器中检查。"
    }
    if status.gatewayServicesActive {
        let recovery = status.recoveryRequired ? " 当前网络状态机尚未结束，退出也不会完成网络恢复。" : ""
        return "退出只会关闭菜单栏图标；正在运行的 DHCP/DNS、mihomo、PF/转发和后台控制服务都不会停止。请先在 Web GUI 中停止网关（如需要）。" + recovery
    }
    return "退出只会关闭菜单栏图标；网关当前未运行，但 OpenSurge 后台控制服务仍会继续运行。"
}

func openSurgeQuitWarning(for status: MenuBarStatus?) -> String {
    guard let status else {
        return "当前无法确认网关服务状态。请先重新连接后台服务，确认网关与网络恢复状态后再退出 OpenSurge。"
    }
    guard status.canQuitOpenSurge else {
        if status.recoveryNeedsAttention {
            return "网络恢复尚未完成。请先在网络设置中完成恢复，再退出 OpenSurge。"
        }
        return "网关数据面仍在运行。请先在网络设置中停止网关，确认 DHCP/DNS、mihomo 与 PF 已停止。"
    }
    return "网关数据面已经停止。此操作会退出菜单栏 App 和用户级 Control Service；由系统 launchd 托管的 root Helper 仍保持空闲加载，下次打开 OpenSurge 不需要再次授权。"
}

func uninstallWarning(for status: MenuBarStatus?) -> String {
    guard let status else {
        return "当前无法确认网关状态。请先重新连接后台服务，再卸载 OpenSurge。"
    }
    guard status.canUninstall else {
        return "网关仍在运行。请先在网络设置中停止网关，再卸载 OpenSurge。"
    }
    return "网关已经停止。卸载不会修改当前系统的 IPv4 forwarding 状态。"
}
