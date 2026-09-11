import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { getDiagnostics, getGuide, getRecordings, removeRecording, schedule, startLive, stopLive } from './api'
import { GuideGrid } from './GuideGrid'
import { readResumePositions, writeResumePositions } from './resume'
import type { Diagnostics, Guide, Program, Recording } from './types'

type View = 'guide' | 'recordings'
type Toast = { tone: 'success' | 'error'; message: string }
type Playback = { src: string; title: string; startAt: number; recordingId?: number; liveSessionId?: string }

const HlsPlayer = lazy(() => import('./HlsPlayer').then((module) => ({ default: module.HlsPlayer })))
const APP_VERSION = '1.11'

function floorHalfHour(date: Date) {
  const result = new Date(date)
  result.setMinutes(result.getMinutes() < 30 ? 0 : 30, 0, 0)
  return result
}

function when(start: string, end: string) {
  const formatter = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' })
  return `${formatter.format(new Date(start))}–${new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(new Date(end))}`
}

function Empty({ children }: { children: React.ReactNode }) { return <div className="empty"><span>◌</span><p>{children}</p></div> }

export default function App() {
  const [view, setView] = useState<View>('guide')
  const [from, setFrom] = useState(() => floorHalfHour(new Date()))
  const to = useMemo(() => new Date(from.getTime() + 6 * 60 * 60 * 1000), [from])
  const [guide, setGuide] = useState<Guide | null>(null)
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null)
  const [selected, setSelected] = useState<Program | null>(null)
  const [playing, setPlaying] = useState<Playback | null>(null)
  const [resumePositions, setResumePositions] = useState(readResumePositions)
  const [liveStarting, setLiveStarting] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Set<number>>(() => new Set())
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState<Toast | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [nextGuide, nextRecordings, nextDiagnostics] = await Promise.all([getGuide(from, to), getRecordings(), getDiagnostics()])
      setGuide(nextGuide); setRecordings(nextRecordings); setDiagnostics(nextDiagnostics)
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not reach the DVR' })
    } finally { setLoading(false) }
  }, [from, to])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const next = await getDiagnostics()
        if (active) setDiagnostics(next)
      } catch {
        if (active) setDiagnostics(null)
      }
    }
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])
  useEffect(() => {
    if (view !== 'recordings') return
    let active = true
    const refresh = async () => {
      try {
        const next = await getRecordings()
        if (active) setRecordings(next)
      } catch (error) {
        if (active) setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not refresh recordings' })
      }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 4000)
    return () => { active = false; window.clearInterval(timer) }
  }, [view])
  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), 4500)
    return () => window.clearTimeout(timer)
  }, [toast])

  useEffect(() => {
    if (!playing?.liveSessionId) return
    const sessionId = playing.liveSessionId
    const cleanup = () => { void stopLive(sessionId, true) }
    window.addEventListener('pagehide', cleanup)
    return () => window.removeEventListener('pagehide', cleanup)
  }, [playing?.liveSessionId])

  const scheduled = useMemo(() => new Set(recordings.filter((recording) => recording.status === 'scheduled' || recording.status === 'recording').map((recording) => recording.programId)), [recordings])

  const record = useCallback(async (program: Program) => {
    try {
      const recording = await schedule(program.id)
      setRecordings((current) => [...current, recording])
      setSelected(null)
      setToast({ tone: 'success', message: `${program.title} is scheduled` })
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not schedule recording' })
      throw error
    }
  }, [])

  const rememberPosition = useCallback((recordingId: number, seconds: number) => {
    if (seconds < 2) return
    setResumePositions((current) => {
      if (Math.abs((current[String(recordingId)] ?? 0) - seconds) < 1) return current
      const next = { ...current, [String(recordingId)]: seconds }
      writeResumePositions(next)
      return next
    })
  }, [])

  async function watchLive(program: Program) {
    setLiveStarting(program.id)
    try {
      const session = await startLive(program.channel.number, program.title)
      setSelected(null)
      setPlaying({ src: `/recordings/${session.playlistPath}`, title: session.title || program.title, startAt: 0, liveSessionId: session.id })
      void getDiagnostics().then(setDiagnostics).catch(() => undefined)
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not start live TV' })
    } finally {
      setLiveStarting(null)
    }
  }

  function closePlayer() {
    const sessionId = playing?.liveSessionId
    setPlaying(null)
    if (sessionId) {
      void stopLive(sessionId).then(() => getDiagnostics().then(setDiagnostics)).catch((error) => {
        setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not stop live TV' })
      })
    }
  }

  useEffect(() => {
    const context = document.modelContext
    if (!context?.registerTool || !guide) return
    const lifecycle = new AbortController()
    const registration = context.registerTool({
      name: 'create_recording',
      title: 'Schedule a recording',
      description: 'Schedule one currently loaded guide program by its exact program ID, using the configured padding and tuner conflict checks.',
      inputSchema: {
        type: 'object',
        properties: { programId: { type: 'string', description: 'Exact ID from the loaded program guide.' } },
        required: ['programId'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      async execute(input) {
        const programId = typeof input === 'object' && input !== null && 'programId' in input ? (input as { programId?: unknown }).programId : undefined
        if (typeof programId !== 'string') throw new Error('programId must be a string')
        const program = guide.programs.find((candidate) => candidate.id === programId)
        if (!program) throw new Error('That program is not in the loaded guide window')
        if (scheduled.has(program.id)) return { status: 'already_scheduled', programId: program.id, title: program.title }
        await record(program)
        return { status: 'scheduled', programId: program.id, title: program.title }
      },
    }, { signal: lifecycle.signal })
    void Promise.resolve(registration).catch(() => undefined)
    return () => lifecycle.abort()
  }, [guide, record, scheduled])

  async function deleteJob(recording: Recording) {
    setDeleting((current) => new Set(current).add(recording.id))
    try {
      await removeRecording(recording.id)
      setRecordings((current) => current.filter((item) => item.id !== recording.id))
      if (playing?.recordingId === recording.id) setPlaying(null)
      setResumePositions((current) => {
        if (!(String(recording.id) in current)) return current
        const next = { ...current }
        delete next[String(recording.id)]
        writeResumePositions(next)
        return next
      })
      setToast({ tone: 'success', message: `${recording.title} removed` })
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not remove recording' })
    } finally {
      setDeleting((current) => {
        const next = new Set(current)
        next.delete(recording.id)
        return next
      })
    }
  }

  const titleDate = new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'short', day: 'numeric' }).format(from)
  const inferredUsed = recordings.filter((recording) => recording.status === 'recording').length + (playing?.liveSessionId ? 1 : 0)
  const tunerCount = diagnostics?.tunerCount ?? 0
  const tunersInUse = diagnostics?.tunersInUse ?? Math.min(tunerCount, inferredUsed)
  const tunersAvailable = diagnostics?.tunersAvailable ?? Math.max(0, tunerCount - tunersInUse)

  return (
    <div className="app-shell">
      <header className="topbar">
        <button className="brand" onClick={() => setView('guide')} aria-label="CatchUp home"><img src="/icon.svg" alt="" /><span>CatchUp</span></button>
        <nav aria-label="Primary">
          <button className={view === 'guide' ? 'active' : ''} onClick={() => setView('guide')}>Guide</button>
          <button className={view === 'recordings' ? 'active' : ''} onClick={() => setView('recordings')}>Recordings <span className="count">{recordings.length}</span></button>
        </nav>
        <div className="system-status" title={diagnostics?.gpuWarning || (diagnostics ? `${tunersInUse} tuners in use` : 'System offline')}>
          <span className="tuner-lights" aria-hidden="true">{Array.from({ length: tunerCount }, (_, index) => <i key={index} className={index < tunersInUse ? 'busy' : 'online'} />)}</span>
          {diagnostics ? `${tunersAvailable} of ${tunerCount} free` : 'Offline'}
        </div>
      </header>

      <main>
        {view === 'guide' ? (
          <>
            <div className="page-heading">
              <div><span className="eyebrow">Live TV</span><h1>{titleDate}</h1></div>
              <div className="date-controls">
                <button onClick={() => setFrom((date) => new Date(date.getTime() - 3 * 60 * 60 * 1000))} aria-label="Earlier programs">‹</button>
                <button onClick={() => setFrom(floorHalfHour(new Date()))}>Now</button>
                <button onClick={() => setFrom((date) => new Date(date.getTime() + 3 * 60 * 60 * 1000))} aria-label="Later programs">›</button>
              </div>
            </div>
            {loading ? <div className="loading-grid" aria-label="Loading guide" /> : guide && guide.channels.length ? (
              <GuideGrid channels={guide.channels} programs={guide.programs} from={from} to={to} scheduled={scheduled} selected={selected} onSelect={setSelected} />
            ) : <Empty>No guide data yet. Refresh the guide from the server diagnostics.</Empty>}
          </>
        ) : (
          <>
            <div className="page-heading"><div><span className="eyebrow">Your library</span><h1>Recordings</h1></div></div>
            {recordings.length ? <div className="recording-list">{recordings.map((recording) => (
              <article key={recording.id} className="recording-card">
                <div className={`status-art ${recording.status}`}><span>{recording.status === 'recording' ? 'REC' : recording.channelNumber}</span></div>
                <div className="recording-info"><span className="status-label">{recording.status}</span><h2>{recording.title}</h2><p>{when(recording.programStart, recording.programEnd)} · Channel {recording.channelNumber}</p>{recording.errorMessage && <p className="recording-error">{recording.errorMessage}</p>}</div>
                <div className="recording-actions">
                  {recording.playlistPath && <button className="primary" onClick={() => setPlaying({ src: `/recordings/${recording.playlistPath}`, title: recording.title, startAt: resumePositions[String(recording.id)] ?? 0, recordingId: recording.id })}>{(resumePositions[String(recording.id)] ?? 0) >= 2 ? 'Resume' : recording.status === 'recording' ? 'Watch from start' : 'Play'}</button>}
                  <button className="subtle danger" disabled={deleting.has(recording.id)} onClick={() => void deleteJob(recording)}>{deleting.has(recording.id) ? 'Stopping…' : recording.status === 'recording' ? 'Stop & Delete' : 'Delete'}</button>
                </div>
              </article>
            ))}</div> : <Empty>Your scheduled and completed recordings will appear here.</Empty>}
          </>
        )}
      </main>

      <footer className="app-version">CatchUp DVR v{APP_VERSION}</footer>

      {selected && <div className="details-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setSelected(null) }}>
        <aside className="details" aria-label={`${selected.title} details`}>
          <button className="icon-button close" onClick={() => setSelected(null)} aria-label="Close details">×</button>
          <span className="eyebrow">{selected.category || 'Program'} · {selected.channel.number} {selected.channel.name}</span>
          <h2>{selected.title}</h2>
          {selected.subtitle && <h3>{selected.subtitle}</h3>}
          <p className="program-time">{when(selected.start, selected.end)}</p>
          <p className="description">{selected.description || 'No description is available.'}</p>
          <div className="padding-note">Recordings include 2 min before and 5 min after</div>
          <div className="details-actions">
            <button className="primary live-button" disabled={liveStarting === selected.id} onClick={() => void watchLive(selected)}>{liveStarting === selected.id ? 'Starting live TV…' : 'Watch Live'}</button>
            <button className="subtle record-button" disabled={scheduled.has(selected.id)} onClick={() => void record(selected)}><span className="record-icon" />{scheduled.has(selected.id) ? 'Scheduled' : 'Record this program'}</button>
          </div>
        </aside>
      </div>}

      {playing && <Suspense fallback={null}><HlsPlayer src={playing.src} title={playing.title} startAt={playing.startAt} onProgress={playing.recordingId ? (seconds) => rememberPosition(playing.recordingId!, seconds) : undefined} onClose={closePlayer} /></Suspense>}
      {toast && <div className={`toast ${toast.tone}`} role="status">{toast.message}</div>}
    </div>
  )
}
