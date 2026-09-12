import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { castPlaylist, getCastContext } from './cast'

type Props = {
  src: string
  playlistPath: string
  title: string
  startAt?: number
  liveSessionId?: string
  onProgress?: (seconds: number) => void
  onCastingChange?: (casting: boolean) => void
  onClose: () => void
}
type MediaRange = { start: number; end: number }

function isSafari() {
  const userAgent = navigator.userAgent
  return /Safari\//.test(userAgent) && !/(Chrome|Chromium|CriOS|Edg|OPR)\//.test(userAgent)
}

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

export function HlsPlayer({ src, playlistPath, title, startAt = 0, liveSessionId, onProgress, onCastingChange, onClose }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const timelineRef = useRef<MediaRange | null>(null)
  const usesHlsRef = useRef(false)
  const progressRef = useRef(onProgress)
  const positionRef = useRef(0)
  const lastReportedRef = useRef(-1)
  const castContextRef = useRef<any>(undefined)
  const castMediaRef = useRef<any>(undefined)
  const removeCastListenerRef = useRef<(() => void) | undefined>(undefined)
  const removeMediaListenerRef = useRef<(() => void) | undefined>(undefined)
  const [range, setRange] = useState<MediaRange | null>(null)
  const [position, setPosition] = useState(0)
  const [playbackError, setPlaybackError] = useState('')
  const [casting, setCasting] = useState(false)
  const [castBusy, setCastBusy] = useState(false)
  const [castError, setCastError] = useState('')
  const [remotePlaying, setRemotePlaying] = useState(true)

  progressRef.current = onProgress

  function reportProgress(next: number, force = false) {
    positionRef.current = next
    if (!progressRef.current || (!force && Math.abs(next - lastReportedRef.current) < 1)) return
    lastReportedRef.current = next
    progressRef.current(next)
  }

  function updateTimeline(video: HTMLVideoElement) {
    let next = timelineRef.current
    if (!usesHlsRef.current) {
      next = mediaRange(video)
      timelineRef.current = next
      setRange(next)
    }
    if (next) {
      const nextPosition = Math.max(0, Math.min(video.currentTime - next.start, next.end - next.start))
      setPosition(nextPosition)
      reportProgress(nextPosition)
    }
  }

  function seekTo(target: number, play = false) {
    const video = videoRef.current
    const bounds = timelineRef.current ?? (video ? mediaRange(video) : null)
    if (!bounds) return false
    const clamped = Math.max(bounds.start, Math.min(target, Math.max(bounds.start, bounds.end - 0.1)))
    if (casting && castMediaRef.current) {
      const cast = (window as any).chrome?.cast
      if (!cast) return false
      const request = new cast.media.SeekRequest()
      request.currentTime = clamped
      castMediaRef.current.seek(request)
      setPosition(Math.max(0, clamped - bounds.start))
      reportProgress(Math.max(0, clamped - bounds.start))
      if (play) playRemote()
      return true
    }
    if (!video) return false
    video.currentTime = clamped
    updateTimeline(video)
    if (play) void video.play()
    return true
  }

  function startOver(play = true) {
    const bounds = timelineRef.current
    return Boolean(bounds && seekTo(bounds.start + 0.01, play))
  }

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    video.setAttribute('x-webkit-airplay', 'allow')
    timelineRef.current = null
    usesHlsRef.current = false
    positionRef.current = 0
    lastReportedRef.current = -1
    setRange(null)
    setPosition(0)
    setPlaybackError('')
    let hls: Hls | undefined
    let retry: number | undefined
    let stopRetry: number | undefined
    let establishNativeStart: (() => void) | undefined
    const timelineEvents: Array<keyof HTMLMediaElementEventMap> = []

    const nativeHls = Boolean(video.canPlayType('application/vnd.apple.mpegurl'))
    const hlsJsSupported = Hls.isSupported()
    if (nativeHls && (isSafari() || !hlsJsSupported)) {
      let startedAtBeginning = false
      const handleNativeStart = () => {
        updateTimeline(video)
        if (!startedAtBeginning && timelineRef.current) {
          startedAtBeginning = true
          seekTo(timelineRef.current.start + Math.max(0.01, startAt))
        }
      }
      establishNativeStart = handleNativeStart
      timelineEvents.push('loadedmetadata', 'durationchange', 'progress', 'canplay')
      timelineEvents.forEach((event) => video.addEventListener(event, handleNativeStart))
      retry = window.setInterval(handleNativeStart, 250)
      stopRetry = window.setTimeout(() => {
        if (retry !== undefined) window.clearInterval(retry)
      }, 15_000)
      video.src = src
      video.load()
    } else if (hlsJsSupported) {
      usesHlsRef.current = true
      hls = new Hls({
        autoStartLoad: false,
        startPosition: startAt,
        liveDurationInfinity: true,
        liveSyncDurationCount: 3,
        backBufferLength: 60 * 60 * 8,
      })
      hls.on(Hls.Events.MANIFEST_PARSED, () => hls?.startLoad(startAt))
      hls.on(Hls.Events.LEVEL_UPDATED, (_event, data) => {
        const next = { start: data.details.fragmentStart, end: data.details.edge }
        timelineRef.current = next
        setRange(next)
        updateTimeline(video)
      })
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) setPlaybackError(`Playback could not continue: ${data.details}`)
      })
      hls.loadSource(src)
      hls.attachMedia(video)
    } else {
      setPlaybackError('This browser cannot play HLS video.')
    }

    return () => {
      reportProgress(positionRef.current, true)
      if (retry !== undefined) window.clearInterval(retry)
      if (stopRetry !== undefined) window.clearTimeout(stopRetry)
      const handleNativeStart = establishNativeStart
      if (handleNativeStart) {
        timelineEvents.forEach((event) => video.removeEventListener(event, handleNativeStart))
      }
      hls?.destroy()
      video.removeAttribute('src')
      video.load()
    }
  }, [src, startAt])

  function jump(seconds: number) {
    const video = videoRef.current
    const bounds = timelineRef.current
    if (!bounds) return
    const current = casting ? bounds.start + positionRef.current : video?.currentTime
    if (current === undefined) return
    seekTo(current + seconds)
  }

  function goLive() {
    const bounds = timelineRef.current
    if (bounds) seekTo(bounds.end - 1, true)
  }

  function playRemote() {
    const cast = (window as any).chrome?.cast
    if (!cast || !castMediaRef.current) return
    castMediaRef.current.play(new cast.media.PlayRequest())
    setRemotePlaying(true)
  }

  function pauseRemote() {
    const cast = (window as any).chrome?.cast
    if (!cast || !castMediaRef.current) return
    castMediaRef.current.pause(new cast.media.PauseRequest())
    setRemotePlaying(false)
  }

  function togglePlayback() {
    if (casting) {
      if (remotePlaying) pauseRemote()
      else playRemote()
      return
    }
    const video = videoRef.current
    if (!video) return
    if (video.paused) void video.play()
    else video.pause()
  }

  function clearCasting() {
    removeCastListenerRef.current?.()
    removeCastListenerRef.current = undefined
    removeMediaListenerRef.current?.()
    removeMediaListenerRef.current = undefined
    castMediaRef.current = undefined
    setCasting(false)
    setRemotePlaying(true)
    onCastingChange?.(false)
  }

  async function startCasting() {
    setCastBusy(true)
    setCastError('')
    try {
      const media = await castPlaylist({ playlistPath, liveSessionId, title, startAt: positionRef.current })
      const context = await getCastContext()
      castContextRef.current = context
      castMediaRef.current = media
      const updateMedia = (alive: boolean) => {
        if (!alive) return
        const next = Number(media.currentTime)
        if (Number.isFinite(next)) {
          const bounds = timelineRef.current
          const offset = bounds ? Math.max(0, next - bounds.start) : Math.max(0, next)
          setPosition(offset)
          reportProgress(offset)
        }
        setRemotePlaying(media.playerState !== (window as any).chrome?.cast?.media?.PlayerState?.PAUSED)
      }
      media.addUpdateListener(updateMedia)
      removeMediaListenerRef.current = () => media.removeUpdateListener(updateMedia)
      const sessionStateChanged = (event: any) => {
        if (event.sessionState === (window as any).cast?.framework?.SessionState?.SESSION_ENDED) clearCasting()
      }
      context.addEventListener((window as any).cast.framework.CastContextEventType.SESSION_STATE_CHANGED, sessionStateChanged)
      removeCastListenerRef.current?.()
      removeCastListenerRef.current = () => context.removeEventListener((window as any).cast.framework.CastContextEventType.SESSION_STATE_CHANGED, sessionStateChanged)
      videoRef.current?.pause()
      setCasting(true)
      onCastingChange?.(true)
    } catch (error) {
      setCastError(error instanceof Error ? error.message : 'Could not start Google Cast.')
    } finally {
      setCastBusy(false)
    }
  }

  async function stopCasting() {
    setCastBusy(true)
    try {
      await castContextRef.current?.endCurrentSession(true)
    } catch (error) {
      setCastError(error instanceof Error ? error.message : 'Could not stop Google Cast.')
    } finally {
      clearCasting()
      setCastBusy(false)
    }
  }

  function closePlayer() {
    if (casting) {
      void stopCasting().finally(onClose)
      return
    }
    onClose()
  }

  useEffect(() => () => {
    removeCastListenerRef.current?.()
    removeMediaListenerRef.current?.()
    onCastingChange?.(false)
  }, [onCastingChange])

  const recordedDuration = range ? Math.max(0, range.end - range.start) : 0
  const atLive = Boolean(range && range.end - (range.start + position) < 8)

  return (
    <div className="player-overlay" role="dialog" aria-modal="true" aria-label={`Playing ${title}`}>
      <div className="player-shell">
        <div className="player-heading">
          <div><span className="eyebrow">Now playing</span><h2>{title}</h2></div>
          <div className="player-heading-actions">
            {!casting && (
              <button type="button" className="cast-button" disabled={castBusy} onClick={() => void startCasting()}>
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M1 18v3h3a3 3 0 0 0-3-3Zm0-4v2a5 5 0 0 1 5 5h2a7 7 0 0 0-7-7Zm0-4v2c5 0 9 4 9 9h2c0-6.1-4.9-11-11-11Zm3-7a3 3 0 0 0-3 3v2h2V6c0-.6.4-1 1-1h16c.6 0 1 .4 1 1v12c0 .6-.4 1-1 1h-6v2h6a3 3 0 0 0 3-3V6a3 3 0 0 0-3-3H4Z" /></svg>
                {castBusy ? 'Connecting…' : 'Cast to TV'}
              </button>
            )}
            <button type="button" className="icon-button" onClick={closePlayer} aria-label="Close player">×</button>
          </div>
        </div>
        <video ref={videoRef} className={casting ? 'cast-source' : ''} controls={!casting} playsInline preload="auto" onTimeUpdate={(event) => updateTimeline(event.currentTarget)} onSeeking={(event) => updateTimeline(event.currentTarget)} />
        {casting && <div className="cast-status"><span aria-hidden="true">▣</span><div><strong>Casting to your TV</strong><p>Playback controls stay here in CatchUp.</p></div><button type="button" className="subtle" disabled={castBusy} onClick={() => void stopCasting()}>{castBusy ? 'Stopping…' : 'Stop casting'}</button></div>}
        <div className="playback-position" aria-live="off">{range ? `${formatOffset(position)} of ${formatOffset(recordedDuration)} recorded` : 'Preparing recorded timeline…'}</div>
        {playbackError && <div className="player-error" role="alert">{playbackError}</div>}
        {castError && <div className="player-error" role="alert">{castError}</div>}
        <div className="transport" aria-label="Playback jumps">
          <button type="button" disabled={casting ? !castMediaRef.current : false} onClick={togglePlayback}>{casting ? (remotePlaying ? 'Pause' : 'Play') : 'Play / Pause'}</button>
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
