// Hash routing: no server-side fallback is needed and the app works under any
// reverse-proxy prefix.
import { shareTargetHash } from './share'

export type Route = 'queue' | 'history' | 'add' | 'settings' | 'server-settings'

const ROUTES: Route[] = ['queue', 'history', 'add', 'settings', 'server-settings']

function parse(hash: string): Route {
  const name = hash.replace(/^#\/?/, '').split('?')[0] ?? ''
  return (ROUTES as string[]).includes(name) ? (name as Route) : 'queue'
}

function parseQuery(hash: string): string {
  const i = hash.indexOf('?')
  return i < 0 ? '' : hash.slice(i + 1)
}

const shared = shareTargetHash(location.search)
if (shared) history.replaceState(null, '', location.pathname + shared)

class Router {
  route = $state<Route>(parse(location.hash))
  /** The query of the current hash route, e.g. `url=…` for `#/add?url=…`. */
  query = $state(parseQuery(location.hash))
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
      this.query = parseQuery(next)
      window.scrollTo(0, 0)
    })
  }

  get params(): URLSearchParams {
    return new URLSearchParams(this.query)
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
