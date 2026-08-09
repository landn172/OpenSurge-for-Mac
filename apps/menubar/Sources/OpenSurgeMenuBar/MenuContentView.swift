import AppKit
import ServiceManagement
import SwiftUI

// MARK: - Menu metrics

// A macOS menu is a command list, not a settings card: one grid, 13pt rows,
// a fixed leading glyph column, and a trailing column for values and key
// equivalents. Every row in this file shares these numbers.
private enum MenuMetrics {
    static let width: CGFloat = 320
    static let rowHeight: CGFloat = 24
    static let rowInset: CGFloat = 5          // highlight rect distance from the menu edge
    static let rowRadius: CGFloat = 5
    static let rowLeading: CGFloat = 8        // inside the highlight rect
    static let rowTrailing: CGFloat = 9
    static let iconColumn: CGFloat = 15
    static let iconGap: CGFloat = 7
    static let contentInset: CGFloat = 12
    static let separatorInset: CGFloat = 10
}

// MARK: - Row highlight propagation

// The accent fill has to flip *every* piece of text in the row, not just the
// label. Trailing values left at `.secondary` on an accent background is the
// classic contrast failure, so the highlight state travels down the subtree.
private struct MenuRowHighlightKey: EnvironmentKey {
    static let defaultValue = false
}

extension EnvironmentValues {
    fileprivate var menuRowHighlighted: Bool {
        get { self[MenuRowHighlightKey.self] }
        set { self[MenuRowHighlightKey.self] = newValue }
    }
}

// MARK: - Row parts

private enum MenuRowIcon {
    case none
    case symbol(String)
    case checkmark(Bool)
    case badge
}

private struct MenuGlyph: View {
    let icon: MenuRowIcon
    @Environment(\.menuRowHighlighted) private var highlighted
    @Environment(\.isEnabled) private var isEnabled

    var body: some View {
        ZStack {
            switch icon {
            case .none:
                Color.clear
            case .symbol(let name):
                Image(systemName: name).font(.system(size: 12))
            case .checkmark(let isOn):
                Image(systemName: "checkmark")
                    .font(.system(size: 11, weight: .semibold))
                    .opacity(isOn ? 1 : 0)
            case .badge:
                Circle().frame(width: 7, height: 7)
            }
        }
        .frame(width: MenuMetrics.iconColumn, height: MenuMetrics.iconColumn)
        .foregroundStyle(tint)
    }

    private var tint: AnyShapeStyle {
        if highlighted { return AnyShapeStyle(.white) }
        guard isEnabled else { return AnyShapeStyle(.tertiary) }
        switch icon {
        case .badge: return AnyShapeStyle(Color.accentColor)
        case .checkmark: return AnyShapeStyle(.primary)
        case .none, .symbol: return AnyShapeStyle(.secondary)
        }
    }
}

private struct MenuSecondaryLabel: View {
    private let text: String
    private let usesTabularDigits: Bool
    @Environment(\.menuRowHighlighted) private var highlighted
    @Environment(\.isEnabled) private var isEnabled

    init(_ text: String, tabularDigits: Bool = false) {
        self.text = text
        self.usesTabularDigits = tabularDigits
    }

    var body: some View {
        Text(text)
            .font(usesTabularDigits ? .system(size: 12.5).monospacedDigit() : .system(size: 12.5))
            .lineLimit(1)
            .truncationMode(.tail)
            .foregroundStyle(tint)
    }

    private var tint: AnyShapeStyle {
        // 95% white keeps the trailing column recessive while still clearing
        // 4.5:1 against the accent fill.
        if highlighted { return AnyShapeStyle(Color.white.opacity(0.95)) }
        return isEnabled ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tertiary)
    }
}

private struct MenuRowLabel: View {
    let title: String
    var icon: MenuRowIcon = .none
    var value: String?
    var keyEquivalent: String?
    var trailingSymbol: String?
    var isDefault = false

    var body: some View {
        HStack(spacing: MenuMetrics.iconGap) {
            MenuGlyph(icon: icon)
            // No negative tracking here: the labels are Chinese, which is
            // already set on a fixed em grid.
            Text(title)
                .font(.system(size: 13, weight: isDefault ? .semibold : .regular))
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            if let value { MenuSecondaryLabel(value, tabularDigits: true) }
            if let keyEquivalent { MenuSecondaryLabel(keyEquivalent) }
            if let trailingSymbol {
                MenuGlyph(icon: .symbol(trailingSymbol))
                    .frame(width: 12)
            }
        }
    }
}

private struct MenuRowButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        RowBody(configuration: configuration)
    }

    // Not named `Body`: that collides with ButtonStyle's associated type.
    private struct RowBody: View {
        let configuration: ButtonStyleConfiguration
        @Environment(\.isEnabled) private var isEnabled
        @State private var hovering = false

        // Disabled rows never highlight — that is native behaviour, and it is
        // the state a quick pass over hover styling usually misses.
        private var highlighted: Bool { isEnabled && (hovering || configuration.isPressed) }

        var body: some View {
            configuration.label
                .padding(.leading, MenuMetrics.rowLeading)
                .padding(.trailing, MenuMetrics.rowTrailing)
                .frame(height: MenuMetrics.rowHeight)
                .frame(maxWidth: .infinity, alignment: .leading)
                .foregroundStyle(labelTint)
                .background(
                    RoundedRectangle(cornerRadius: MenuMetrics.rowRadius, style: .continuous)
                        .fill(highlighted ? Color.accentColor : Color.clear)
                )
                .contentShape(Rectangle())
                .environment(\.menuRowHighlighted, highlighted)
                .onHover { hovering = $0 }
                .padding(.horizontal, MenuMetrics.rowInset)
        }

        private var labelTint: AnyShapeStyle {
            if highlighted { return AnyShapeStyle(.white) }
            return isEnabled ? AnyShapeStyle(.primary) : AnyShapeStyle(.tertiary)
        }
    }
}

// MARK: - Submenu flyout

// The popover has to stop dismissing itself while one of its own rows is
// tracking an AppKit menu, otherwise clicking a submenu item reads as a click
// outside the popover and tears the whole panel down.
extension Notification.Name {
    static let openSurgeFlyoutWillOpen = Notification.Name("OpenSurgeMenuFlyoutWillOpen")
    static let openSurgeFlyoutDidClose = Notification.Name("OpenSurgeMenuFlyoutDidClose")
}

private final class MenuFlyoutAnchor {
    weak var view: NSView?
    /// Keeps the menu (and therefore its action items' targets) alive past the
    /// `popUp` call so a late action dispatch still has something to send to.
    var activeMenu: NSMenu?
}

/// Exposes the row's backing NSView so an NSMenu can be anchored to it.
private struct MenuFlyoutAnchorView: NSViewRepresentable {
    let anchor: MenuFlyoutAnchor

    // Flipped so the anchor point below reads top-down like the layout does.
    final class FlippedView: NSView {
        override var isFlipped: Bool { true }
    }

    func makeNSView(context: Context) -> FlippedView {
        let view = FlippedView(frame: .zero)
        anchor.view = view
        return view
    }

    func updateNSView(_ nsView: FlippedView, context: Context) {
        anchor.view = nsView
    }
}

private final class MenuFlyoutActionItem: NSMenuItem {
    private let handler: () -> Void

    init(title: String, handler: @escaping () -> Void) {
        self.handler = handler
        super.init(title: title, action: #selector(fire), keyEquivalent: "")
        target = self
    }

    required init(coder: NSCoder) {
        fatalError("MenuFlyoutActionItem is never restored from a nib")
    }

    @objc private func fire() { handler() }
}

private enum MenuFlyoutItem {
    case info(label: String, value: String)
    case separator
    case command(title: String, handler: () -> Void)

    func makeItem() -> NSMenuItem {
        switch self {
        case .separator:
            return .separator()
        case .info(let label, let value):
            let item = NSMenuItem(title: "\(label)：\(value)", action: nil, keyEquivalent: "")
            item.isEnabled = false
            return item
        case .command(let title, let handler):
            return MenuFlyoutActionItem(title: title, handler: handler)
        }
    }
}

private struct MenuFlyoutRow: View {
    let title: String
    let systemImage: String
    var help = ""
    let items: [MenuFlyoutItem]

    @State private var anchor = MenuFlyoutAnchor()

    var body: some View {
        MenuRow(
            title: title,
            icon: .symbol(systemImage),
            trailingSymbol: "chevron.right",
            help: help
        ) {
            presentFlyout()
        }
        .background(MenuFlyoutAnchorView(anchor: anchor))
    }

    private func presentFlyout() {
        guard let view = anchor.view, view.window != nil else { return }

        let menu = NSMenu()
        menu.autoenablesItems = false
        for item in items { menu.addItem(item.makeItem()) }
        anchor.activeMenu = menu

        // `popUp` runs a modal tracking loop, so `defer` fires once the menu closes.
        NotificationCenter.default.post(name: .openSurgeFlyoutWillOpen, object: nil)
        defer { NotificationCenter.default.post(name: .openSurgeFlyoutDidClose, object: nil) }

        // Just past the row's trailing edge. AppKit flips the menu to the left
        // itself when it would otherwise run off the screen.
        let origin = NSPoint(
            x: view.bounds.maxX - MenuMetrics.rowInset,
            y: view.bounds.minY - MenuMetrics.rowInset
        )
        menu.popUp(positioning: nil, at: origin, in: view)
    }
}

private struct MenuRow: View {
    let title: String
    var icon: MenuRowIcon = .none
    var value: String?
    var keyEquivalent: String?
    var trailingSymbol: String?
    var isDefault = false
    var help = ""
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            MenuRowLabel(
                title: title,
                icon: icon,
                value: value,
                keyEquivalent: keyEquivalent,
                trailingSymbol: trailingSymbol,
                isDefault: isDefault
            )
        }
        .buttonStyle(MenuRowButtonStyle())
        .help(help)
    }
}

// MARK: - Non-interactive blocks

private struct MenuHeader: View {
    let indicator: IndicatorState
    let subtitle: String

    var body: some View {
        HStack(spacing: 10) {
            OpenSurgeAppIconView(size: 29)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                Text("OpenSurge for Mac").font(.system(size: 13, weight: .semibold))
                HStack(spacing: 5) {
                    Circle()
                        .fill(indicator.statusDotColor)
                        .frame(width: 6, height: 6)
                    Text(subtitle)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, MenuMetrics.contentInset)
        .padding(.top, 5)
        .padding(.bottom, 9)
        .accessibilityElement(children: .combine)
    }
}

private struct MenuInfoRow: View {
    let label: String
    let value: String
    var monospaced = false
    var dot: Color?

    var body: some View {
        HStack(spacing: 12) {
            Text(label).font(.system(size: 12.5)).foregroundStyle(.secondary)
            Spacer(minLength: 8)
            HStack(spacing: 6) {
                if let dot {
                    Circle().fill(dot).frame(width: 6, height: 6)
                }
                Text(value)
                    .font(monospaced ? .system(size: 12, design: .monospaced) : .system(size: 12.5).monospacedDigit())
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
        }
        .frame(height: 20)
        // Warnings and TUN errors can outrun the row; keep the full text reachable.
        .help(value)
    }
}

private struct MenuInfoBlock<Content: View>: View {
    @ViewBuilder let content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content() }
            .padding(.horizontal, MenuMetrics.contentInset)
            .padding(.bottom, 6)
    }
}

private struct MenuNoteBlock: View {
    let tone: Color
    let symbol: String
    let title: String
    let message: String

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: symbol)
                .font(.system(size: 12))
                .foregroundStyle(tone)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 12, weight: .semibold))
                // Chinese body copy: leading stays well above the Latin default.
                Text(message)
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
                    .lineSpacing(6)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(tone.opacity(0.14), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .padding(.horizontal, MenuMetrics.separatorInset)
        .padding(.bottom, 6)
    }
}

// The one control in this panel that is not a menu row. The gateway is the
// thing the app exists to turn on, so it gets a real switch at the top rather
// than a checkmark buried in the command list.
private struct MenuSwitchRow: View {
    let title: String
    let state: GatewaySwitchState
    let action: (Bool) -> Void

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.system(size: 13, weight: .semibold))
                Text(state.subtitle)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            Spacer(minLength: 8)
            Toggle("", isOn: Binding(get: { state.isOn }, set: action))
                .labelsHidden()
                .toggleStyle(.switch)
                .controlSize(.small)
                .disabled(!state.isEnabled)
                .accessibilityLabel(title)
        }
        .padding(.horizontal, MenuMetrics.contentInset)
        .frame(height: 32)
        .help(state.help)
    }
}

private struct MenuSeparator: View {
    var body: some View {
        Divider()
            .padding(.horizontal, MenuMetrics.separatorInset)
            .padding(.vertical, 5)
    }
}

// MARK: - Menu content

struct MenuContentView: View {
    @ObservedObject var model: StatusModel

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            MenuHeader(indicator: model.indicator, subtitle: headerSubtitle)

            MenuSwitchRow(title: gatewaySwitchTitle, state: model.gatewaySwitch) { isOn in
                setGatewayRunning(isOn)
            }

            MenuSeparator()
            statusSection

            MenuSeparator()
            commandSection

            MenuSeparator()
            MenuRow(
                title: "打开 App 时自动启动",
                icon: .checkmark(model.autoStartGateway),
                help: autoStartHelp
            ) {
                model.autoStartGateway.toggle()
            }

            MenuRow(
                title: "登录时显示",
                icon: .checkmark(model.openAtLogin),
                help: "登录时自动打开 OpenSurge 菜单栏 App"
            ) {
                setOpenAtLogin(!model.openAtLogin)
            }

            MenuSeparator()
            updateSection

            MenuSeparator()
            MenuRow(
                title: "退出 OpenSurge…",
                icon: .symbol("power"),
                keyEquivalent: "⌘Q",
                help: fullQuitHelp
            ) {
                confirmQuit(.openSurge)
            }
            .disabled(!model.canQuitOpenSurge)
            .keyboardShortcut("q", modifiers: .command)

            MenuRow(
                title: "只退出菜单栏 App…",
                icon: .symbol("rectangle.portrait.and.arrow.right"),
                keyEquivalent: "⌥⌘Q",
                help: "只关闭菜单栏图标，后台控制服务继续运行"
            ) {
                confirmQuit(.menuBarOnly)
            }
            .disabled(model.isChangingServices)
            .keyboardShortcut("q", modifiers: [.command, .option])

            MenuSeparator()
            MenuRow(
                title: model.isUninstalling ? "正在卸载…" : "卸载 OpenSurge…",
                icon: .symbol("trash"),
                help: uninstallHelp
            ) {
                confirmUninstall()
            }
            .disabled(!model.canUninstall)
        }
        .padding(.vertical, 5)
        .frame(width: MenuMetrics.width)
        .onAppear { model.startPolling(rapid: true) }
        .onDisappear { model.stopRapidPolling() }
    }

    // MARK: Sections

    @ViewBuilder
    private var statusSection: some View {
        if let status = model.status {
            if status.recoveryNeedsAttention {
                MenuNoteBlock(
                    tone: .orange,
                    symbol: "exclamationmark.triangle.fill",
                    title: "网络已开始变更",
                    message: "完成状态机并验证路由器 DHCP 恢复前，不要把 Mac 切回自动获取。"
                )
                MenuInfoBlock {
                    MenuInfoRow(
                        label: "当前阶段",
                        value: recoveryStageLabel(status.recoveryStage ?? "required")
                    )
                }
            } else {
                MenuInfoBlock {
                    MenuInfoRow(label: "局域网 IP", value: status.lanIp, monospaced: true)
                    MenuInfoRow(label: "已连接客户端", value: "\(status.clientCount) 台")
                    // Drift can co-occur with takeover. The subtitle is a single
                    // truncating line, so the actionable text lives on its own row
                    // rather than losing a race with the takeover copy.
                    if status.drift {
                        MenuInfoRow(label: "配置", value: "已修改 · 需重启网关", dot: .orange)
                    }
                }
            }
        } else {
            MenuNoteBlock(
                tone: model.error == nil ? .secondary : .red,
                symbol: model.error == nil ? "network" : "network.slash",
                title: model.error == nil ? "正在连接后台控制服务" : "后台控制服务没有响应",
                message: model.error ?? "读取到网关状态之前，依赖状态的命令暂时不可用。"
            )
        }

        if let error = model.error, model.status != nil {
            MenuNoteBlock(
                tone: .red,
                symbol: "exclamationmark.circle.fill",
                title: "控制服务返回错误",
                message: error
            )
        }
    }

    @ViewBuilder
    private var commandSection: some View {
        if model.status == nil, model.serviceNeedsReconnect {
            MenuRow(
                title: "重新连接后台服务",
                icon: .symbol("dot.radiowaves.left.and.right"),
                isDefault: true
            ) {
                Task { await model.reconnectService() }
            }
            .disabled(model.isRefreshing)
        }

        if let status = model.status, status.runtimeInterrupted {
            // The switch cannot offer start over a runtime left by the previous
            // boot; this is the one command that clears it.
            MenuRow(
                title: "安全清理旧状态",
                icon: .symbol("bandage"),
                isDefault: true,
                help: "只清理上次开机留下的状态，不会向旧 PID 发送信号，也不会改动本次开机的 PF 或 IPv4 转发"
            ) {
                confirmCleanupInterruptedRuntime()
            }
            .disabled(model.pendingGatewayAction != nil)
        }

        if let status = model.status, status.recoveryRequired, status.recoveryNeedsAttention {
            // No snapshot badge here: `recoveryNeedsAttention` excludes the
            // "prepared" stage, so `recoverySnapshotPrepared` can never be true
            // on this row. That state is carried by the header subtitle instead.
            MenuRow(
                title: "继续恢复网络设置",
                icon: .symbol("wrench.and.screwdriver"),
                isDefault: true
            ) {
                Task { await model.openWebGUI(path: "network") }
            }
        }

        // Exactly one default item per menu: whichever recovery command is
        // showing takes precedence, otherwise the panel does.
        MenuRow(
            title: "打开 OpenSurge 面板",
            icon: .symbol("arrow.up.forward.app"),
            keyEquivalent: "⌘O",
            isDefault: model.status != nil && !hasRecoveryDefaultItem
        ) {
            Task { await model.openWebGUI() }
        }
        .disabled(model.status == nil)
        .keyboardShortcut("o", modifiers: .command)

        if let status = model.status {
            MenuFlyoutRow(
                title: "详细状态",
                systemImage: "list.bullet",
                help: "DHCP / DNS、mihomo、TUN、PF 与转发状态",
                items: detailItems(status)
            )
        } else {
            MenuRow(
                title: "详细状态",
                icon: .symbol("list.bullet"),
                trailingSymbol: "chevron.right",
                help: "需要先连接后台服务"
            ) {}
                .disabled(true)
        }

        MenuRow(
            title: "刷新状态",
            icon: .symbol("arrow.clockwise"),
            keyEquivalent: "⌘R"
        ) {
            Task { await model.refresh() }
        }
        .disabled(model.isRefreshing)
        .keyboardShortcut("r", modifiers: .command)

        MenuRow(
            title: "复制诊断摘要",
            icon: .symbol("doc.on.doc"),
            keyEquivalent: "⌥⌘C"
        ) {
            model.copyDiagnostics()
        }
        .keyboardShortcut("c", modifiers: [.command, .option])
    }

    private func detailItems(_ status: MenuBarStatus) -> [MenuFlyoutItem] {
        var items: [MenuFlyoutItem] = [
            .info(label: "DHCP / DNS", value: status.dhcp),
            .info(label: "mihomo", value: status.mihomo),
            .info(label: "TUN", value: tunLabel(status)),
            .info(label: "PF 锚点", value: status.pfAnchor),
            .info(label: "IPv4 转发", value: status.forwarding),
        ]
        if let tunError = status.tunError, !tunError.isEmpty {
            items.append(.separator)
            items.append(.info(label: "TUN 错误", value: tunError))
        }
        if !status.warnings.isEmpty {
            items.append(.separator)
            items.append(contentsOf: status.warnings.map { .info(label: "警告", value: $0) })
        }
        items.append(.separator)
        items.append(.command(title: "在面板中打开网络设置", handler: {
            Task { await model.openWebGUI(path: "network") }
        }))
        return items
    }

    @ViewBuilder
    private var updateSection: some View {
        MenuRow(
            title: model.isCheckingForUpdate ? "正在检查更新…" : "检查更新…",
            icon: .symbol("arrow.down.circle"),
            value: model.updateCheckMessage ?? model.currentVersion,
            help: model.updateCheckMessage ?? "当前版本 \(model.currentVersion)"
        ) {
            Task { await model.checkForUpdates() }
        }
        .disabled(model.isCheckingForUpdate)

        if let update = model.availableUpdate {
            MenuRow(title: "下载 \(update.version) 更新…", icon: .badge) {
                model.openUpdateDownloadPage()
            }
        }
    }

    // MARK: Derived copy

    private var hasRecoveryDefaultItem: Bool {
        if model.status == nil, model.serviceNeedsReconnect { return true }
        // An interrupted runtime carries no recovery state for the bypass
        // topologies, so it has to be named here or the cleanup row and the
        // panel row would both render as the default.
        if model.status?.runtimeInterrupted == true { return true }
        return model.status?.recoveryNeedsAttention == true
    }

    private var gatewaySwitchTitle: String {
        model.status?.topologyLabel ?? "网关"
    }

    private var autoStartHelp: String {
        guard let status = model.status, !status.gatewaySwitchable else {
            return "局域网 DHCP 接管不会自动启动；它只能在面板的网络设置中按恢复流程进行"
        }
        return "打开 OpenSurge 后，如果\(status.topologyLabel)已停止且没有待处理的网络恢复，就自动启动它"
    }

    private var headerSubtitle: String {
        guard let status = model.status else {
            return model.isRefreshing && model.error == nil ? "正在连接控制服务…" : "无法连接控制服务"
        }
        if status.recoveryNeedsAttention { return "网络恢复尚未完成" }
        if status.takeoverActive { return takeoverStatusLabel(status.recoveryStage) }
        if status.recoverySnapshotPrepared { return "恢复资料已备份 · 尚未改动网络" }
        // `drift` is not handled here — it gets its own info row so it survives
        // alongside takeover copy instead of being swallowed by an early return.
        return "\(gatewayLabel(status.gateway)) · \(status.topologyLabel)"
    }

    private var fullQuitHelp: String {
        if model.status?.recoveryNeedsAttention == true { return "请先完成网络恢复" }
        if model.status?.canQuitOpenSurge != true { return "请先在网络设置中停止网关" }
        return "停止用户级 Control Service，然后退出菜单栏 App；root Helper 保持空闲加载"
    }

    private var uninstallHelp: String {
        if model.status == nil { return "请先重新连接后台服务" }
        if model.status?.canUninstall != true { return "请先在网络设置中停止网关" }
        return "移除 OpenSurge App、后台服务与 root Helper"
    }

    private func tunLabel(_ status: MenuBarStatus) -> String {
        guard let interface = status.tunInterface else { return status.tun ?? "unknown" }
        return "\(status.tun ?? "unknown") · \(interface)"
    }

    // MARK: Actions

    private func setOpenAtLogin(_ enabled: Bool) {
        model.openAtLogin = enabled
        try? enabled ? SMAppService.mainApp.register() : SMAppService.mainApp.unregister()
    }

    // Starting is what the switch is for, so it never asks. Stopping does ask
    // once downstream clients are actually leaning on this Mac for DHCP/DNS and
    // routing, because that flip drops them.
    private func setGatewayRunning(_ running: Bool) {
        if !running, let status = model.status, status.clientCount > 0 {
            guard gatewayStopConfirmation(clientCount: status.clientCount).present() else { return }
        }
        model.setGatewayRunning(running)
    }

    private func confirmCleanupInterruptedRuntime() {
        guard interruptedRuntimeCleanupConfirmation().present() else { return }
        model.cleanupInterruptedRuntime()
    }

    private func confirmQuit(_ confirmation: QuitConfirmation) {
        guard confirmation.present(for: model.status) else { return }
        switch confirmation {
        case .openSurge:
            model.quitOpenSurge()
        case .menuBarOnly:
            model.quitMenuBarApp()
        }
    }

    private func confirmUninstall() {
        guard let mode = UninstallConfirmation.present(for: model.status) else { return }
        model.uninstall(mode)
    }
}

// MARK: - Status tone

extension IndicatorState {
    fileprivate var statusDotColor: Color {
        switch self {
        case .running: .green
        case .degraded, .recovery: .orange
        case .unreachable: .red
        case .stopped, .connecting: .secondary
        }
    }
}

private func gatewayLabel(_ gateway: String) -> String {
    switch gateway {
    case "running": "网关正在运行"
    case "stopped": "网关已停止"
    case "degraded": "网关运行异常"
    default: "网关 \(gateway)"
    }
}

@MainActor
private enum UninstallConfirmation {
    static func present(for status: MenuBarStatus?) -> UninstallMode? {
        guard status?.canUninstall == true else { return nil }

        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "卸载 OpenSurge？"
        alert.informativeText = """
        OpenSurge App、用户级 Control Service 与 root Helper 都会被移除。

        “保留数据并卸载”会保留配置、订阅和策略数据，便于重新安装；“彻底卸载”还会删除凭据、运行记录和日志。当前系统的 IPv4 forwarding 状态不会被修改。
        """
        alert.addButton(withTitle: "保留数据并卸载")
        alert.addButton(withTitle: "彻底卸载").hasDestructiveAction = true
        alert.addButton(withTitle: "取消")

        switch alert.runModal() {
        case .alertFirstButtonReturn: return .keepData
        case .alertSecondButtonReturn: return .removeEverything
        default: return nil
        }
    }
}

/// The copy is plain data so the checks can assert it; only presenting it needs
/// the main actor.
struct GatewayConfirmation {
    let title: String
    let message: String
    let buttonTitle: String

    @MainActor
    func present() -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = message
        alert.addButton(withTitle: buttonTitle).hasDestructiveAction = true
        alert.addButton(withTitle: "取消")
        return alert.runModal() == .alertFirstButtonReturn
    }
}

func gatewayStopConfirmation(clientCount: Int) -> GatewayConfirmation {
    GatewayConfirmation(
        title: "停止网关？",
        message: "当前有 \(clientCount) 台下游设备在使用这台 Mac 的 DHCP/DNS 与代理转发，停止后它们会立即失去这些服务。",
        buttonTitle: "停止网关"
    )
}

func interruptedRuntimeCleanupConfirmation() -> GatewayConfirmation {
    GatewayConfirmation(
        title: "安全清理上次开机留下的状态？",
        message: "OpenSurge 不会向旧 PID 发送信号，也不会更改本次开机的 PF 或 IPv4 转发。如果上次运行启用了系统代理协同，将恢复为 OpenSurge 启动前保存的状态。",
        buttonTitle: "安全清理"
    )
}

@MainActor
private enum QuitConfirmation {
    case menuBarOnly
    case openSurge

    var title: String {
        self == .openSurge ? "退出 OpenSurge？" : "只退出菜单栏 App？"
    }

    var buttonTitle: String {
        self == .openSurge ? "退出 OpenSurge" : "仍然退出"
    }

    func message(for status: MenuBarStatus?) -> String {
        self == .openSurge
            ? openSurgeQuitWarning(for: status)
            : menuBarQuitWarning(for: status)
    }

    func present(for status: MenuBarStatus?) -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = message(for: status)
        alert.addButton(withTitle: buttonTitle).hasDestructiveAction = true
        alert.addButton(withTitle: "取消")
        return alert.runModal() == .alertFirstButtonReturn
    }
}

private func recoveryStageLabel(_ stage: String) -> String {
    switch stage {
    case "mac_static": "Mac 已使用固定 IPv4"
    case "router_dhcp_disabled_confirmed": "路由器 DHCP 已关闭"
    case "gateway_active": "OpenSurge 已接管"
    case "client_validated": "客户端 DHCP、DNS 与 TUN 已验收"
    case "client_validation_skipped": "客户端验收已跳过"
    case "gateway_stopped_waiting_router_dhcp": "已停止，等待恢复路由器 DHCP"
    case "router_dhcp_restored": "路由器 DHCP 已恢复"
    default: stage
    }
}

private func takeoverStatusLabel(_ stage: String?) -> String {
    switch stage {
    case "client_validated": "局域网 DHCP 接管已验收"
    case "client_validation_skipped": "接管运行中 · 验收已跳过"
    default: "接管运行中 · 等待客户端验收"
    }
}
