/**
 * Monoline 1.7px-stroke icons for the navigation rail, drawn with
 * `currentColor` so the rail's hover and active colours apply without a second
 * set of assets. Rendered once as a hidden sprite; each rail button references
 * a symbol by id.
 */
export function RailIconSprite() {
  return <svg width="0" height="0" aria-hidden="true" focusable="false" style={{ position: 'absolute' }}>
    <defs>
      <symbol id="icon-dashboard" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="7.5" height="7.5" rx="1.5" /><rect x="13.5" y="3" width="7.5" height="7.5" rx="1.5" />
        <rect x="3" y="13.5" width="7.5" height="7.5" rx="1.5" /><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5" />
      </symbol>
      <symbol id="icon-connectivity" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.6 2.5 15.4 0 18M12 3c-2.5 2.6-2.5 15.4 0 18" />
      </symbol>
      <symbol id="icon-diagnostics" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 12h4l2.5-6 4 12 2.5-6H21" />
      </symbol>
      <symbol id="icon-network" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <path d="M4.5 9a10 10 0 0 1 15 0M7.5 12.5a6 6 0 0 1 9 0" /><circle cx="12" cy="17.5" r="1.5" />
      </symbol>
      <symbol id="icon-sources" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <path d="m12 3 8.5 4.5L12 12 3.5 7.5 12 3Z" /><path d="m3.5 12.5 8.5 4.5 8.5-4.5" />
      </symbol>
      <symbol id="icon-paired" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <rect x="6" y="2.5" width="12" height="19" rx="2.5" /><path d="M10.5 5.5h3" />
        <path d="M9.5 13.2l1.8 1.8 3.2-3.4" />
      </symbol>
      <symbol id="icon-devices" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <rect x="2.5" y="4" width="19" height="12" rx="1.8" /><path d="M8.5 20h7M12 16v4" />
      </symbol>
      <symbol id="icon-policies" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 7h11l-3-3M21 17H10l3 3" />
      </symbol>
      <symbol id="icon-theme" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="5" />
        <path d="M12 2v2M12 20v2M4.2 4.2l1.5 1.5M18.3 18.3l1.5 1.5M2 12h2M20 12h2M4.2 19.8l1.5-1.5M18.3 5.7l1.5-1.5" />
      </symbol>
    </defs>
  </svg>
}

export function RailIcon({ name }: { name: string }) {
  return <svg aria-hidden="true" focusable="false"><use href={`#icon-${name}`} /></svg>
}
