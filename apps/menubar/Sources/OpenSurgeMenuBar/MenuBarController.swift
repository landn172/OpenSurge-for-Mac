import AppKit
import Combine
import SwiftUI

@MainActor
protocol MenuBarPresenting: AnyObject {
    func showPanel()
    func applicationDidBecomeActive()
}

extension MenuBarPresenting {
    func applicationDidBecomeActive() {}
}

enum MenuBarPanelPresentationAction: Equatable {
    case none
    case waitForAnchor
    case showPopover
    case waitForPanelWindow
    case replacePopover
    case complete
}

enum MenuBarPanelRetryPolicy {
    static let presentationWindow: TimeInterval = 8
    static let popoverWindowGrace: TimeInterval = 1

    static func delay(after attempt: Int) -> TimeInterval {
        switch attempt {
        case ..<5:
            return 0.05
        case ..<10:
            return 0.1
        default:
            return 0.25
        }
    }
}

func menuBarPanelPresentationAction(
    presentationPending: Bool,
    anchorVisible: Bool,
    popoverShown: Bool,
    panelWindowAvailable: Bool,
    popoverWindowWaitExpired: Bool
) -> MenuBarPanelPresentationAction {
    guard presentationPending else { return .none }
    guard anchorVisible else { return .waitForAnchor }
    guard popoverShown else { return .showPopover }
    guard panelWindowAvailable else {
        return popoverWindowWaitExpired ? .replacePopover : .waitForPanelWindow
    }
    return .complete
}

// The panel hosts AppKit submenus (详细状态), which live in their own window.
// Under `.transient` AppKit reads a click in that window as a click outside the
// popover and tears the panel down mid-interaction — and whether mutating
// `behavior` on an already-shown popover retracts that monitor is undocumented.
// So the panel owns dismissal in *both* activation states, via the monitors
// below, which are suspended for as long as a submenu is tracking. The
// parameter is kept to pin the fact that activation state deliberately no
// longer changes the answer.
func menuBarPopoverBehavior(applicationActive: Bool) -> NSPopover.Behavior {
    .applicationDefined
}

func menuBarStatusItemNeedsRefresh(
    renderedIndicator: IndicatorState?,
    nextIndicator: IndicatorState
) -> Bool {
    renderedIndicator != nextIndicator
}

@MainActor
final class OpenSurgeAppDelegate: NSObject, NSApplicationDelegate {
    private var presenter: (any MenuBarPresenting)?

    override init() {
        super.init()
    }

    init(presenter: any MenuBarPresenting) {
        self.presenter = presenter
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        if presenter == nil {
            presenter = MenuBarController(model: StatusModel())
        }

        // Every process launch opens the same panel as the menu bar icon.
        // This also makes the "登录时显示" setting literal: login-item
        // launches present the panel instead of starting silently.
        presenter?.showPanel()
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        presenter?.applicationDidBecomeActive()
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        presenter?.showPanel()
        return false
    }
}

@MainActor
final class MenuBarController: NSObject, MenuBarPresenting {
    private let model: StatusModel
    private let statusItem: NSStatusItem
    private var popover = NSPopover()
    private var modelObservation: AnyCancellable?
    private var presentationPending = false
    private var presentationRetryDeadline: Date?
    private var presentationRetryAttempt = 0
    private var presentationRetryScheduled = false
    private var popoverWindowDeadline: Date?
    private var panelPresented = false
    private var renderedIndicator: IndicatorState?
    private var outsideClickMonitor: Any?
    private var escapeKeyMonitor: Any?
    private var finalPresentationVerificationScheduled = false
    private var flyoutObservers: [NSObjectProtocol] = []
    private var flyoutTracking = false

    init(model: StatusModel) {
        self.model = model
        self.statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        super.init()

        configurePopover(
            popover,
            contentController: makePanelContentController()
        )

        if let button = statusItem.button {
            button.target = self
            button.action = #selector(togglePanel(_:))
            button.sendAction(on: [.leftMouseUp])
            // NSChangeBackgroundCellMask is imported without a Swift member
            // name in the macOS 14 SDK used by this project.
            (button.cell as? NSButtonCell)?.showsStateBy = NSCell.StyleMask(rawValue: 1 << 3)
        }
        updateStatusItem()
        modelObservation = model.objectWillChange.sink { [weak self] _ in
            DispatchQueue.main.async {
                self?.updateStatusItem()
            }
        }
        installFlyoutObservers()
    }

    deinit {
        for observer in flyoutObservers {
            NotificationCenter.default.removeObserver(observer)
        }
    }

    // A submenu opened from a panel row lives in its own window. Under
    // `.transient` AppKit reads a click there as a click outside the popover and
    // tears the panel down, so dismissal is suspended for as long as the
    // submenu is tracking.
    //
    // `queue: nil` is deliberate — it delivers synchronously on the posting
    // thread, so suspension is in place before `popUp` starts tracking. Passing
    // `.main` here would enqueue the block instead and reopen the very gap this
    // closes. The only poster is a main-thread SwiftUI action, which is what
    // makes `assumeIsolated` sound.
    private func installFlyoutObservers() {
        let center = NotificationCenter.default
        flyoutObservers = [
            center.addObserver(
                forName: .openSurgeFlyoutWillOpen,
                object: nil,
                queue: nil
            ) { [weak self] _ in
                MainActor.assumeIsolated { self?.beginFlyoutTracking() }
            },
            center.addObserver(
                forName: .openSurgeFlyoutDidClose,
                object: nil,
                queue: nil
            ) { [weak self] _ in
                MainActor.assumeIsolated { self?.endFlyoutTracking() }
            },
        ]
    }

    private func beginFlyoutTracking() {
        guard !flyoutTracking else { return }
        flyoutTracking = true
        popover.behavior = .applicationDefined
        removeApplicationDefinedDismissMonitors()
    }

    private func endFlyoutTracking() {
        guard flyoutTracking else { return }
        flyoutTracking = false
        guard popover.isShown else { return }
        preparePopoverForCurrentActivationState()
    }

    func showPanel() {
        cancelFinalPresentationVerification()
        presentationPending = true
        presentationRetryDeadline = Date().addingTimeInterval(
            MenuBarPanelRetryPolicy.presentationWindow
        )
        presentationRetryAttempt = 0
        requestApplicationActivation()
        advancePanelPresentation()
    }

    func applicationDidBecomeActive() {
        advancePanelPresentation()
        promoteVisiblePanelForActiveApplication()
    }

    @objc
    private func togglePanel(_ sender: Any?) {
        if popover.isShown,
           popover.contentViewController?.view.window != nil {
            cancelPendingPresentation()
            popover.performClose(sender)
        } else {
            showPanel()
        }
    }

    private func advancePanelPresentation() {
        cancelScheduledPresentationRetry()
        guard presentationPending else { return }
        let now = Date()
        let button = statusItem.button
        let panelWindow = popover.contentViewController?.view.window
        let action = menuBarPanelPresentationAction(
            presentationPending: presentationPending,
            anchorVisible: statusItem.isVisible
                && button?.window?.isVisible == true
                && button?.window?.screen != nil
                && button?.isHiddenOrHasHiddenAncestor == false,
            popoverShown: popover.isShown,
            panelWindowAvailable: panelWindow != nil,
            popoverWindowWaitExpired: popoverWindowDeadline.map { now >= $0 } ?? true
        )

        switch action {
        case .none:
            return
        case .waitForAnchor:
            schedulePresentationRetry()
        case .showPopover:
            guard let button else {
                schedulePresentationRetry()
                return
            }
            preparePopoverForCurrentActivationState()
            let windowDeadline = Date().addingTimeInterval(
                MenuBarPanelRetryPolicy.popoverWindowGrace
            )
            popoverWindowDeadline = windowDeadline
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            schedulePresentationRetry(notBefore: windowDeadline)
        case .waitForPanelWindow:
            schedulePresentationRetry(notBefore: popoverWindowDeadline)
        case .replacePopover:
            replaceFailedPopover()
            schedulePresentationRetry()
        case .complete:
            completePanelPresentation()
        }
    }

    private func requestApplicationActivation() {
        // Activation is a best-effort enhancement. Presentation never waits
        // for this request because cooperative activation may be declined.
        if #available(macOS 14.0, *) {
            NSApplication.shared.activate()
        } else {
            NSApplication.shared.activate(ignoringOtherApps: true)
        }
    }

    private func replaceFailedPopover() {
        popoverWindowDeadline = nil
        setPanelPresented(false)
        removeApplicationDefinedDismissMonitors()

        // A failed show can leave NSPopover logically shown without ever
        // creating a window. Replace that invalid presentation object instead
        // of depending on a didClose callback that might never arrive.
        let failedPopover = popover
        failedPopover.delegate = nil
        failedPopover.close()
        let replacement = NSPopover()
        configurePopover(
            replacement,
            contentController: makePanelContentController()
        )
        popover = replacement
    }

    private func makePanelContentController() -> NSViewController {
        let contentController = NSHostingController(
            rootView: MenuContentView(model: model)
                .environment(\.controlActiveState, .key)
        )
        contentController.sizingOptions = [.preferredContentSize]
        return contentController
    }

    private func configurePopover(
        _ candidate: NSPopover,
        contentController: NSViewController
    ) {
        candidate.contentViewController = contentController
        candidate.behavior = .applicationDefined
        candidate.animates = true
        candidate.delegate = self
    }

    private func preparePopoverForCurrentActivationState() {
        guard !flyoutTracking else { return }
        popover.behavior = menuBarPopoverBehavior(
            applicationActive: NSApplication.shared.isActive
        )
        installApplicationDefinedDismissMonitors()
    }

    private func installApplicationDefinedDismissMonitors() {
        if outsideClickMonitor == nil {
            outsideClickMonitor = NSEvent.addGlobalMonitorForEvents(
                matching: [.leftMouseDown, .rightMouseDown, .otherMouseDown]
            ) { [weak self] _ in
                DispatchQueue.main.async {
                    self?.closePanel()
                }
            }
        }
        if escapeKeyMonitor == nil {
            escapeKeyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) {
                [weak self] event in
                // These monitors are now armed whenever the panel is up, so
                // Escape must only be swallowed when the panel is the thing it
                // should close. A quit or uninstall confirmation owns Escape
                // while it is modal, and a closed panel has no business
                // consuming the key at all.
                guard event.keyCode == 53,
                      let self,
                      self.popover.isShown,
                      NSApplication.shared.modalWindow == nil else {
                    return event
                }
                self.closePanel()
                return nil
            }
        }
    }

    private func removeApplicationDefinedDismissMonitors() {
        if let outsideClickMonitor {
            NSEvent.removeMonitor(outsideClickMonitor)
            self.outsideClickMonitor = nil
        }
        if let escapeKeyMonitor {
            NSEvent.removeMonitor(escapeKeyMonitor)
            self.escapeKeyMonitor = nil
        }
    }

    private func promoteVisiblePanelForActiveApplication() {
        guard !flyoutTracking,
              NSApplication.shared.isActive,
              let panelWindow = popover.contentViewController?.view.window else {
            return
        }
        panelWindow.makeKey()
        // Activation no longer hands dismissal back to AppKit; the monitors stay
        // armed so a submenu click can never be mistaken for a click outside.
        installApplicationDefinedDismissMonitors()
    }

    private func schedulePresentationRetry(notBefore: Date? = nil) {
        guard presentationPending,
              !presentationRetryScheduled,
              let retryDeadline = presentationRetryDeadline else {
            return
        }

        let now = Date()
        let remaining = retryDeadline.timeIntervalSince(now)
        guard remaining > 0 else {
            finishTimedOutPanelPresentation()
            return
        }

        var delay = MenuBarPanelRetryPolicy.delay(after: presentationRetryAttempt)
        if let notBefore {
            delay = max(delay, notBefore.timeIntervalSince(now))
        }
        delay = max(0.01, min(delay, remaining))

        presentationRetryScheduled = true
        presentationRetryAttempt += 1
        perform(
            #selector(retryPanelPresentation),
            with: nil,
            afterDelay: delay,
            inModes: [.common]
        )
    }

    @objc
    private func retryPanelPresentation() {
        presentationRetryScheduled = false
        advancePanelPresentation()
    }

    private func cancelScheduledPresentationRetry() {
        guard presentationRetryScheduled else { return }
        NSObject.cancelPreviousPerformRequests(
            withTarget: self,
            selector: #selector(retryPanelPresentation),
            object: nil
        )
        presentationRetryScheduled = false
    }

    private func finishTimedOutPanelPresentation() {
        let button = statusItem.button
        let anchorVisible = statusItem.isVisible
            && button?.window?.isVisible == true
            && button?.window?.screen != nil
            && button?.isHiddenOrHasHiddenAncestor == false

        if popover.contentViewController?.view.window != nil {
            completePanelPresentation()
            return
        }
        if popover.isShown {
            replaceFailedPopover()
        }

        clearPendingPresentation()
        guard anchorVisible, let button else {
            setPanelPresented(false)
            return
        }

        // One final non-blocking attempt avoids leaving a pending state behind.
        // If AppKit still declines it, the next explicit click starts fresh.
        preparePopoverForCurrentActivationState()
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        if popover.contentViewController?.view.window != nil {
            completePanelPresentation()
        } else {
            scheduleFinalPresentationVerification()
        }
    }

    private func completePanelPresentation() {
        cancelFinalPresentationVerification()
        clearPendingPresentation()
        popoverWindowDeadline = nil
        setPanelPresented(true)
        promoteVisiblePanelForActiveApplication()
    }

    private func cancelPendingPresentation() {
        cancelFinalPresentationVerification()
        clearPendingPresentation()
        popoverWindowDeadline = nil
        setPanelPresented(false)
        removeApplicationDefinedDismissMonitors()
    }

    private func scheduleFinalPresentationVerification() {
        cancelFinalPresentationVerification()
        finalPresentationVerificationScheduled = true
        perform(
            #selector(verifyFinalPanelPresentation),
            with: nil,
            afterDelay: MenuBarPanelRetryPolicy.popoverWindowGrace,
            inModes: [.common]
        )
    }

    @objc
    private func verifyFinalPanelPresentation() {
        finalPresentationVerificationScheduled = false
        if popover.contentViewController?.view.window != nil {
            completePanelPresentation()
            return
        }
        if popover.isShown {
            replaceFailedPopover()
        }
        setPanelPresented(false)
        removeApplicationDefinedDismissMonitors()
    }

    private func cancelFinalPresentationVerification() {
        guard finalPresentationVerificationScheduled else { return }
        NSObject.cancelPreviousPerformRequests(
            withTarget: self,
            selector: #selector(verifyFinalPanelPresentation),
            object: nil
        )
        finalPresentationVerificationScheduled = false
    }

    private func clearPendingPresentation() {
        presentationPending = false
        presentationRetryDeadline = nil
        presentationRetryAttempt = 0
        cancelScheduledPresentationRetry()
    }

    private func closePanel() {
        cancelPendingPresentation()
        if popover.isShown {
            popover.performClose(nil)
        }
    }

    private func setPanelPresented(_ presented: Bool) {
        panelPresented = presented
        applyStatusItemPresentedState()
    }

    private func applyStatusItemPresentedState() {
        guard let button = statusItem.button else { return }
        button.state = panelPresented ? .on : .off
    }

    private func updateStatusItem() {
        guard let button = statusItem.button else { return }
        let indicator = model.indicator
        guard menuBarStatusItemNeedsRefresh(
            renderedIndicator: renderedIndicator,
            nextIndicator: indicator
        ) else {
            applyStatusItemPresentedState()
            return
        }
        renderedIndicator = indicator
        button.image = openSurgeMenuBarImage(for: indicator)
        button.imagePosition = .imageOnly
        button.alphaValue = indicator.menuBarIconOpacity
        button.toolTip = indicator.accessibilityLabel
        button.setAccessibilityLabel(indicator.accessibilityLabel)
        applyStatusItemPresentedState()
    }
}

extension MenuBarController: NSPopoverDelegate {
    func popoverDidShow(_ notification: Notification) {
        popoverWindowDeadline = nil
        if popover.contentViewController?.view.window != nil {
            completePanelPresentation()
        } else {
            advancePanelPresentation()
        }
    }

    func popoverDidClose(_ notification: Notification) {
        cancelPendingPresentation()
    }
}
