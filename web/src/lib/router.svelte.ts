// Hash routing: no server-side fallback is needed and the app works under any
// reverse-proxy prefix.

export type Route = 'queue' | 'history' | 'add' | 'settings' | 'server-settings'

const ROUTES: Route[] = ['queue', 'history', 'add', 'settings', 'server-settings']

function parse(hash: string): Route {
  const name = hash.replace(/^#\/?/, '').split('?')[0] ?? ''
  return (ROUTES as string[]).includes(name) ? (name as Route) : 'queue'
}

class Router {
  route = $state<Route>(parse(location.hash))
  /** Returns false to keep the user on the current page (unsaved edits). */
  #guard: (() => boolean) | null = null
  #current = location.hash

  constructor() {
    window.addEventListener('hashchange', () => {
      const next = location.hash
      if (this.#guard && !this.#guard()) {
        history.replaceState(null, '', this.#current || '#/queue')
        return
      }
      this.#guard = null
      this.#current = next
      this.route = parse(next)
      window.scrollTo(0, 0)
    })
  }

  go(route: Route): void {
    if (this.route === route && location.hash === `#/${route}`) return
    location.hash = `#/${route}`
  }

  setGuard(fn: (() => boolean) | null): void {
    this.#guard = fn
  }
}

export const router = new Router()

export const href = (route: Route) => `#/${route}`
