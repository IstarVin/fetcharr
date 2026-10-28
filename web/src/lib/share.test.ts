import { describe, expect, it } from 'vitest'
import { bookmarklet, firstHttpUrl, pairingLink, serverAddress, shareTargetHash } from './share'

describe('share target', () => {
  it('prefers url, then the first URL in text', () => {
    expect(shareTargetHash('?url=https%3A%2F%2Fa.example%2Fv%3Fx%3D1')).toBe('#/add?url=https%3A%2F%2Fa.example%2Fv%3Fx%3D1')
    expect(shareTargetHash('?title=T&text=Watch%20this%20https%3A%2F%2Fb.example%2Fe1%20now')).toBe('#/add?url=https%3A%2F%2Fb.example%2Fe1')
    expect(shareTargetHash('?text=no%20link')).toBeNull()
    expect(shareTargetHash('')).toBeNull()
  })

  it('ignores non-http schemes', () => {
    expect(firstHttpUrl('javascript:alert(1)')).toBeNull()
    expect(firstHttpUrl('see <https://x.example/a>')).toBe('https://x.example/a')
  })
})

describe('bookmarklet', () => {
  it('opens the add form for the current page', () => {
    const b = bookmarklet('https://media.example/fetcharr/#/settings')
    expect(b).toBe(`javascript:void(window.open("https://media.example/fetcharr/"+'?url='+encodeURIComponent(location.href)))`)
  })
})

describe('pairing', () => {
  it('builds the connect link with an optional token', () => {
    expect(pairingLink('http://192.168.1.5:8080', '')).toBe('fetcharr://connect?url=http%3A%2F%2F192.168.1.5%3A8080')
    expect(pairingLink('https://m.example/f', ' t&k ')).toBe('fetcharr://connect?url=https%3A%2F%2Fm.example%2Ff&token=t%26k')
  })

  it('uses the page base without a trailing slash', () => {
    expect(serverAddress('http://192.168.1.5:8080/')).toBe('http://192.168.1.5:8080')
    expect(serverAddress('https://m.example/fetcharr/index.html')).toBe('https://m.example/fetcharr')
  })
})
