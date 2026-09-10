import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'

type Props = { src: string; title: string; onClose: () => void }

export function HlsPlayer({ src, title, onClose }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const [atLive, setAtLive] = useState(false)

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    video.setAttribute('x-webkit-airplay', 'allow')
    let hls: Hls | undefined
    if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = src
    } else if (Hls.isSupported()) {
      hls = new Hls({ liveSyncDurationCount: 3, backBufferLength: 60 * 60 * 8 })
      hls.loadSource(src)
      hls.attachMedia(video)
    }
    const startAtBeginning = () => {
      if (video.seekable.length) video.currentTime = video.seekable.start(0)
    }
    video.addEventListener('loadedmetadata', startAtBeginning, { once: true })
    return () => { hls?.destroy(); video.removeEventListener('loadedmetadata', startAtBeginning) }
  }, [src])

  function jump(seconds: number) {
    const video = videoRef.current
    if (!video || !video.seekable.length) return
    const start = video.seekable.start(0)
    const end = video.seekable.end(video.seekable.length - 1)
    video.currentTime = Math.max(start, Math.min(video.currentTime + seconds, end))
  }

  function goLive() {
    const video = videoRef.current
    if (!video || !video.seekable.length) return
    video.currentTime = Math.max(video.seekable.start(0), video.seekable.end(video.seekable.length - 1) - 1)
    void video.play()
  }

  return (
    <div className="player-overlay" role="dialog" aria-modal="true" aria-label={`Playing ${title}`}>
      <div className="player-shell">
        <div className="player-heading"><div><span className="eyebrow">Now playing</span><h2>{title}</h2></div><button className="icon-button" onClick={onClose} aria-label="Close player">×</button></div>
        <video ref={videoRef} controls playsInline onTimeUpdate={(event) => {
          const video = event.currentTarget
          if (video.seekable.length) setAtLive(video.seekable.end(video.seekable.length - 1) - video.currentTime < 8)
        }} />
        <div className="transport" aria-label="Playback jumps">
          <button onClick={() => jump(-10)}>−10</button>
          <button onClick={() => jump(30)}>+30</button>
          <button onClick={() => jump(60)}>+60</button>
          <button className={atLive ? 'live active' : 'live'} onClick={goLive}><span />Go Live</button>
        </div>
      </div>
    </div>
  )
}

