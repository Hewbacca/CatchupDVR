import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'

type Props = { src: string; title: string; onClose: () => void }
type MediaRange = { start: number; end: number }

function mediaRange(video: HTMLVideoElement): MediaRange | null {
  const ranges = video.seekable.length ? video.seekable : video.buffered
  if (!ranges.length) return null
  return { start: ranges.start(0), end: ranges.end(ranges.length - 1) }
}

function formatOffset(seconds: number) {
  const whole = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0
  const hours = Math.floor(whole / 3600)
  const minutes = Math.floor((whole % 3600) / 60)
  const remainder = whole % 60
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}` : `${minutes}:${String(remainder).padStart(2, '0')}`
}

export function HlsPlayer({ src, title, onClose }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const [range, setRange] = useState<MediaRange | null>(null)
  const [position, setPosition] = useState(0)
  const [playbackError, setPlaybackError] = useState('')

  function updateTimeline(video: HTMLVideoElement) {
    const next = mediaRange(video)
    setRange(next)
    if (next) setPosition(Math.max(0, video.currentTime - next.start))
  }

  function seekTo(target: number, play = false) {
    const video = videoRef.current
    if (!video) return false
    const bounds = mediaRange(video)
    if (!bounds) return false
    const clamped = Math.max(bounds.start, Math.min(target, Math.max(bounds.start, bounds.end - 0.1)))
    video.currentTime = clamped
    updateTimeline(video)
    if (play) void video.play()
    return true
  }

  function startOver(play = true) {
    const video = videoRef.current
    const bounds = video && mediaRange(video)
    return Boolean(bounds && seekTo(bounds.start + 0.01, play))
  }

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    video.setAttribute('x-webkit-airplay', 'allow')
    let hls: Hls | undefined
    let startedAtBeginning = false

    const establishStart = () => {
      updateTimeline(video)
      if (!startedAtBeginning && startOver(false)) startedAtBeginning = true
    }
    const timelineEvents: Array<keyof HTMLMediaElementEventMap> = ['loadedmetadata', 'durationchange', 'progress', 'canplay']
    timelineEvents.forEach((event) => video.addEventListener(event, establishStart))
    const retry = window.setInterval(establishStart, 250)
    const stopRetry = window.setTimeout(() => window.clearInterval(retry), 15_000)

    if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = src
      video.load()
    } else if (Hls.isSupported()) {
      hls = new Hls({
        startPosition: 0,
        liveSyncDurationCount: 3,
        backBufferLength: 60 * 60 * 8,
      })
      hls.on(Hls.Events.LEVEL_LOADED, establishStart)
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) setPlaybackError(`Playback could not continue: ${data.details}`)
      })
      hls.loadSource(src)
      hls.attachMedia(video)
    } else {
      setPlaybackError('This browser cannot play HLS video.')
    }

    return () => {
      window.clearInterval(retry)
      window.clearTimeout(stopRetry)
      timelineEvents.forEach((event) => video.removeEventListener(event, establishStart))
      hls?.destroy()
      video.removeAttribute('src')
      video.load()
    }
  }, [src])

  function jump(seconds: number) {
    const video = videoRef.current
    if (!video) return
    const bounds = mediaRange(video)
    if (!bounds) return
    seekTo(video.currentTime + seconds)
  }

  function goLive() {
    const video = videoRef.current
    const bounds = video && mediaRange(video)
    if (bounds) seekTo(bounds.end - 1, true)
  }

  const recordedDuration = range ? Math.max(0, range.end - range.start) : 0
  const atLive = Boolean(range && range.end - (range.start + position) < 8)

  return (
    <div className="player-overlay" role="dialog" aria-modal="true" aria-label={`Playing ${title}`}>
      <div className="player-shell">
        <div className="player-heading"><div><span className="eyebrow">Now playing</span><h2>{title}</h2></div><button type="button" className="icon-button" onClick={onClose} aria-label="Close player">×</button></div>
        <video ref={videoRef} controls playsInline preload="auto" onTimeUpdate={(event) => updateTimeline(event.currentTarget)} onSeeking={(event) => updateTimeline(event.currentTarget)} />
        <div className="playback-position" aria-live="off">{range ? `${formatOffset(position)} of ${formatOffset(recordedDuration)} recorded` : 'Preparing recorded timeline…'}</div>
        {playbackError && <div className="player-error" role="alert">{playbackError}</div>}
        <div className="transport" aria-label="Playback jumps">
          <button type="button" disabled={!range} onClick={() => startOver()}>Start Over</button>
          <button type="button" disabled={!range} onClick={() => jump(-10)}>−10</button>
          <button type="button" disabled={!range} onClick={() => jump(30)}>+30</button>
          <button type="button" disabled={!range} onClick={() => jump(60)}>+60</button>
          <button type="button" disabled={!range} className={atLive ? 'live active' : 'live'} onClick={goLive}><span />Go Live</button>
        </div>
      </div>
    </div>
  )
}
