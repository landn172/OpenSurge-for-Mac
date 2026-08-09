import { createContext } from 'react'

/**
 * The command bar lives in the shell but its content belongs to the page, so
 * pages hand their title and primary action up through these two DOM slots
 * rather than through props on every page component. The elements are null on
 * the very first render and set by callback refs on the commit after it.
 */
export type ShellSlotElements = { title: HTMLElement | null; action: HTMLElement | null }

/**
 * `null` means there is no shell around this page at all — a page rendered on
 * its own, in a test or in isolation. That case has to keep working, so
 * PageHeader falls back to rendering its own header rather than dropping the
 * title and action on the floor. Inside the shell the provider is always
 * present, so a null slot there only ever means "not mounted yet".
 */
export const ShellSlots = createContext<ShellSlotElements | null>(null)
