import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { MANIFEST_PARSED: 'manifestParsed', LEVEL_UPDATED: 'levelUpdated', ERROR: 'error' }
    static isSupported() { return true }
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

vi.mock('./cast', () => ({ castPlaylist: vi.fn(), getCastContext: vi.fn() }))

import { HlsPlayer } from './HlsPlayer'

describe('HlsPlayer', () => {
  it('keeps Cast to TV in the always-visible player header', () => {
    render(<HlsPlayer src="/recordings/test/index.m3u8" playlistPath="test/index.m3u8" title="Test program" onClose={vi.fn()} />)

    const castButton = screen.getByRole('button', { name: 'Cast to TV' })
    expect(castButton.closest('.player-heading-actions')).toBeInTheDocument()
    expect(screen.getByLabelText('Playback jumps')).not.toContainElement(castButton)
  })
})
