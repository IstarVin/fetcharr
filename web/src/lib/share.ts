// Ways into the Add form from outside the app: the installed web app's share
// target, the "Send to Fetcharr" bookmark, and the Android pairing link.

/** The first http(s) URL in text, e.g. a shared "Title https://…" string. */
export function firstHttpUrl(text: string | null | undefined): string | null {
  return text?.match(/https?:\/\/[^\s<>"']+/i)?.[0] ?? null
}

/**
 * The share target and the bookmark open the page with `?url=` (or `?text=`).
 * Returns the matching `#/add?url=` route, or null when there is no URL.
 */
export function shareTargetHash(search: string): string | null {
  const p = new URLSearchParams(search)
  const url = firstHttpUrl(p.get('url')) ?? firstHttpUrl(p.get('text'))
  return url ? `#/add?${new URLSearchParams({ url })}` : null
}

/**
 * A bookmarklet that opens this server's Add form for the current page in a
 * new tab. appUrl is the web UI's address, e.g. `https://media.example/fetcharr/`.
 */
export function bookmarklet(appUrl: string): string {
  const base = appUrl.split('#')[0]!.split('?')[0]!
  return `javascript:void(window.open(${JSON.stringify(base)}+'?url='+encodeURIComponent(location.href)))`
}

/**
 * The `fetcharr://connect` link the Android app opens to fill in its server
 * settings. serverUrl is the web UI's address without a trailing slash.
 */
export function pairingLink(serverUrl: string, token: string): string {
  const p = new URLSearchParams({ url: serverUrl })
  if (token.trim()) p.set('token', token.trim())
  return `fetcharr://connect?${p}`
}

/** The server address the Android app should use: the page's base, without a trailing slash. */
export function serverAddress(baseURI: string): string {
  const u = new URL('.', baseURI)
  return (u.origin + u.pathname).replace(/\/+$/, '')
}
