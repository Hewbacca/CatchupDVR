import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { getAuthStatus, getDiagnostics, getFavoriteChannels, getGuide, getGuideDays, getRecordings, logout, removeRecording, schedule, searchGuide, setFavoriteChannel, startLive, stopLive } from './api'
import { AuthScreen } from './AuthScreen'
import { GuideGrid } from './GuideGrid'
import { readResumePositions, writeResumePositions } from './resume'
import { SettingsPanel } from './SettingsPanel'
import { TunerSetupScreen } from './TunerSetupScreen'
import type { AuthStatus } from './api'
import type { Diagnostics, Guide, Program, Recording } from './types'

type View = 'guide' | 'recordings'
type Toast = { tone: 'success' | 'error'; message: string }
type Playback = { src: string; playlistPath: string; title: string; startAt: number; recordingId?: number; liveSessionId?: string }

const HlsPlayer = lazy(() => import('./HlsPlayer').then((module) => ({ default: module.HlsPlayer })))
const APP_VERSION = '1.17'

function floorHalfHour(date: Date) {
  const result = new Date(date)
  result.setMinutes(result.getMinutes() < 30 ? 0 : 30, 0, 0)
  return result
}

function guideDayValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function guideDayStart(value: string) {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(year, month - 1, day)
}

function guideDayLabel(value: string) {
  return new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'short', day: 'numeric' }).format(guideDayStart(value))
}

function when(start: string, end: string) {
  const formatter = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' })
  return `${formatter.format(new Date(start))}–${new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(new Date(end))}`
}

function isAiringNow(program: Program, now = Date.now()) {
  return new Date(program.start).getTime() <= now && now < new Date(program.end).getTime()
}

function Empty({ children }: { children: React.ReactNode }) { return <div className="empty"><span>◌</span><p>{children}</p></div> }

export default function App() {
  const [view, setView] = useState<View>('guide')
  const [from, setFrom] = useState(() => floorHalfHour(new Date()))
  const to = useMemo(() => new Date(from.getTime() + 6 * 60 * 60 * 1000), [from])
  const [guide, setGuide] = useState<Guide | null>(null)
  const [guideDays, setGuideDays] = useState<string[]>([])
  const [guideSearch, setGuideSearch] = useState('')
  const [searchResults, setSearchResults] = useState<Program[]>([])
  const [searchIndex, setSearchIndex] = useState(0)
  const [activeSearchResultID, setActiveSearchResultID] = useState<string | null>(null)
  const [searching, setSearching] = useState(false)
  const [favoriteChannels, setFavoriteChannels] = useState<Set<string>>(() => new Set())
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null)
  const [selected, setSelected] = useState<Program | null>(null)
  const [playing, setPlaying] = useState<Playback | null>(null)
  const [casting, setCasting] = useState(false)
  const [resumePositions, setResumePositions] = useState(readResumePositions)
  const [liveStarting, setLiveStarting] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Set<number>>(() => new Set())
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState<Toast | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  const [authError, setAuthError] = useState('')

  const loadAuth = useCallback(async () => {
    try {
      setAuthError('')
      setAuth(await getAuthStatus())
    } catch (error) {
      setAuthError(error instanceof Error ? error.message : 'Could not reach CatchUp')
    }
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [nextGuide, nextRecordings, nextDiagnostics, nextGuideDays, nextFavoriteChannels] = await Promise.all([getGuide(from, to), getRecordings(), getDiagnostics(), getGuideDays(), getFavoriteChannels()])
      setGuide(nextGuide); setRecordings(nextRecordings); setDiagnostics(nextDiagnostics); setGuideDays(nextGuideDays); setFavoriteChannels(new Set(nextFavoriteChannels))
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not reach the DVR' })
    } finally { setLoading(false) }
  }, [from, to])

  useEffect(() => { void loadAuth() }, [loadAuth])
  useEffect(() => { if (auth?.authenticated && !auth.tunerSetupRequired) void load() }, [auth?.authenticated, auth?.tunerSetupRequired, load])
  useEffect(() => {
    if (!auth?.authenticated || auth.tunerSetupRequired) return
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
  }, [auth?.authenticated, auth?.tunerSetupRequired])
  useEffect(() => {
    if (!auth?.authenticated || auth.tunerSetupRequired || view !== 'recordings') return
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
  }, [auth?.authenticated, auth?.tunerSetupRequired, view])
  useEffect(() => {
    const query = guideSearch.trim()
    if (!auth?.authenticated || auth.tunerSetupRequired || !query) {
      setSearchResults([])
      setSearchIndex(0)
      setActiveSearchResultID(null)
      setSearching(false)
      return
    }
    let active = true
    setSearchResults([])
    setSearchIndex(0)
    setActiveSearchResultID(null)
    setSelected(null)
    setSearching(true)
    const timer = window.setTimeout(() => {
      void searchGuide(query).then((results) => {
        if (!active) return
        setSearchResults(results)
        setSearching(false)
        if (results.length) navigateSearchResult(results[0])
      }).catch((error) => {
        if (!active) return
        setSearching(false)
        setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not search the guide' })
      })
    }, 250)
    return () => { active = false; window.clearTimeout(timer) }
  }, [auth?.authenticated, auth?.tunerSetupRequired, guideSearch])
  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), 4500)
    return () => window.clearTimeout(timer)
  }, [toast])

  useEffect(() => {
    // A Chromecast keeps reading the temporary HLS buffer without the browser.
    // Its server-side Cast lease will stop it after media reads cease.
    if (!playing?.liveSessionId || casting) return
    const sessionId = playing.liveSessionId
    const cleanup = () => { void stopLive(sessionId, true) }
    window.addEventListener('pagehide', cleanup)
    return () => window.removeEventListener('pagehide', cleanup)
  }, [casting, playing?.liveSessionId])

  const scheduled = useMemo(() => new Set(recordings.filter((recording) => recording.status === 'scheduled' || recording.status === 'recording').map((recording) => recording.programId)), [recordings])
  const favoriteGuideChannels = useMemo(() => guide?.channels.filter((channel) => favoriteChannels.has(channel.id)) ?? [], [favoriteChannels, guide])
  const searchResultIDs = useMemo(() => new Set(searchResults.map((program) => program.id)), [searchResults])

  async function toggleFavorite(channelID: string) {
    const wasFavorite = favoriteChannels.has(channelID)
    setFavoriteChannels((current) => {
      const next = new Set(current)
      if (wasFavorite) next.delete(channelID)
      else next.add(channelID)
      return next
    })
    try {
      await setFavoriteChannel(channelID, !wasFavorite)
    } catch (error) {
      setFavoriteChannels((current) => {
        const next = new Set(current)
        if (wasFavorite) next.add(channelID)
        else next.delete(channelID)
        return next
      })
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not update favorite channel' })
    }
  }

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
    if (!isAiringNow(program)) {
      setToast({ tone: 'error', message: `${program.title} is not airing right now` })
      return
    }
    setLiveStarting(program.id)
    try {
      const session = await startLive(program.channel.number, program.title)
      setSelected(null)
      setPlaying({ src: `/recordings/${session.playlistPath}`, playlistPath: session.playlistPath, title: session.title || program.title, startAt: 0, liveSessionId: session.id })
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
    setCasting(false)
    if (sessionId) {
      void stopLive(sessionId).then(() => getDiagnostics().then(setDiagnostics)).catch((error) => {
        setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not stop live TV' })
      })
    }
  }

  useEffect(() => {
    const context = document.modelContext
    if (!auth?.authenticated || !context?.registerTool || !guide) return
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
  }, [auth?.authenticated, guide, record, scheduled])

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
  const selectedIsAiringNow = selected ? isAiringNow(selected) : false

  function navigateSearchResult(program: Program) {
    setView('guide')
    setFrom(floorHalfHour(new Date(program.start)))
    setActiveSearchResultID(program.id)
  }

  function moveSearchResult(direction: number) {
    if (!searchResults.length) return
    const nextIndex = (searchIndex + direction + searchResults.length) % searchResults.length
    setSearchIndex(nextIndex)
    navigateSearchResult(searchResults[nextIndex])
  }

  async function signOut() {
    const liveSessionID = playing?.liveSessionId
    if (liveSessionID) {
      try { await stopLive(liveSessionID) } catch { /* Session cleanup will be retried by the server on shutdown. */ }
    }
    try {
      await logout()
      setPlaying(null); setSelected(null); setSettingsOpen(false); setGuide(null); setGuideDays([]); setGuideSearch(''); setSearchResults([]); setFavoriteChannels(new Set()); setRecordings([]); setDiagnostics(null); setAuth({ setupRequired: false, authenticated: false, tunerSetupRequired: false })
    } catch (error) {
      setToast({ tone: 'error', message: error instanceof Error ? error.message : 'Could not sign out' })
    }
  }

  if (!auth) return <main className="auth-page"><section className="auth-card auth-loading"><img src="/icon.svg" alt="" className="auth-icon" /><h1>Connecting to CatchUp…</h1>{authError && <><p className="auth-error">{authError}</p><button className="subtle" onClick={() => void loadAuth()}>Try again</button></>}</section><footer className="app-version">CatchUp DVR v{APP_VERSION}</footer></main>
  if (!auth.authenticated) return <><AuthScreen setupRequired={auth.setupRequired} onAuthenticated={() => void loadAuth()} /><footer className="app-version">CatchUp DVR v{APP_VERSION}</footer></>
  if (auth.tunerSetupRequired) return <><TunerSetupScreen onConfigured={() => void loadAuth()} /><footer className="app-version">CatchUp DVR v{APP_VERSION}</footer></>

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
        <button className="settings-button" onClick={() => setSettingsOpen(true)} aria-label="Settings"><span aria-hidden="true">⚙</span><span>Settings</span></button>
        <button className="sign-out" onClick={() => void signOut()}>Sign out</button>
      </header>

      <main>
        {view === 'guide' ? (
          <>
            <div className="page-heading">
              <div><span className="eyebrow">Live TV</span><h1>{titleDate}</h1></div>
              <div className="guide-controls">
                <label className="guide-search"><span className="sr-only">Search available guide</span><input type="search" value={guideSearch} onChange={(event) => setGuideSearch(event.target.value)} placeholder="Search guide" /></label>
                {guideSearch.trim() && <div className="search-results" role="status">
                  {searching ? <span>Searching…</span> : searchResults.length ? <><button onClick={() => moveSearchResult(-1)} aria-label="Previous match">‹</button><span>{searchIndex + 1} of {searchResults.length} {searchResults.length === 1 ? 'match' : 'matches'}</span><button onClick={() => moveSearchResult(1)} aria-label="Next match">›</button></> : <span>No matches</span>}
                </div>}
                <div className="date-controls">
                  <button onClick={() => setFrom((date) => new Date(date.getTime() - 60 * 60 * 1000))} aria-label="One hour earlier">‹</button>
                  <select aria-label="Guide day" value={guideDayValue(from)} onChange={(event) => setFrom(guideDayStart(event.target.value))} disabled={!guideDays.length}>
                    {guideDays.map((day) => <option key={day} value={day}>{guideDayLabel(day)}</option>)}
                  </select>
                  <button onClick={() => setFrom(floorHalfHour(new Date()))}>Now</button>
                  <button onClick={() => setFrom((date) => new Date(date.getTime() + 60 * 60 * 1000))} aria-label="One hour later">›</button>
                </div>
              </div>
            </div>
            {loading ? <div className="loading-grid" aria-label="Loading guide" /> : guide && guide.channels.length ? <>
              {favoriteGuideChannels.length > 0 && <section className="guide-section"><div className="guide-section-heading"><span className="eyebrow">Pinned channels</span><h2>Favorites</h2></div><GuideGrid channels={favoriteGuideChannels} programs={guide.programs} from={from} to={to} scheduled={scheduled} favorites={favoriteChannels} selected={selected} searchResultIDs={searchResultIDs} activeSearchResultID={activeSearchResultID} searchText={guideSearch} onSelect={setSelected} onToggleFavorite={(channelID) => void toggleFavorite(channelID)} /></section>}
              <section className="guide-section"><div className="guide-section-heading"><span className="eyebrow">Complete lineup</span><h2>All channels</h2></div><GuideGrid channels={guide.channels} programs={guide.programs} from={from} to={to} scheduled={scheduled} favorites={favoriteChannels} selected={selected} searchResultIDs={searchResultIDs} activeSearchResultID={activeSearchResultID} searchText={guideSearch} onSelect={setSelected} onToggleFavorite={(channelID) => void toggleFavorite(channelID)} /></section>
            </> : <Empty>No guide data yet. Refresh the guide from the server diagnostics.</Empty>}
          </>
        ) : (
          <>
            <div className="page-heading"><div><span className="eyebrow">Your library</span><h1>Recordings</h1></div></div>
            {recordings.length ? <div className="recording-list">{recordings.map((recording) => (
              <article key={recording.id} className="recording-card">
                <div className={`status-art ${recording.status}`}><span>{recording.status === 'recording' ? 'REC' : recording.channelNumber}</span></div>
                <div className="recording-info"><span className="status-label">{recording.status}</span><h2>{recording.title}</h2><p>{when(recording.programStart, recording.programEnd)} · Channel {recording.channelNumber}</p>{recording.errorMessage && <p className="recording-error">{recording.errorMessage}</p>}</div>
                <div className="recording-actions">
                  {recording.playlistPath && <button className="primary" onClick={() => setPlaying({ src: `/recordings/${recording.playlistPath}`, playlistPath: recording.playlistPath!, title: recording.title, startAt: resumePositions[String(recording.id)] ?? 0, recordingId: recording.id })}>{(resumePositions[String(recording.id)] ?? 0) >= 2 ? 'Resume' : recording.status === 'recording' ? 'Watch from start' : 'Play'}</button>}
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
            {selectedIsAiringNow && <button className="primary live-button" disabled={liveStarting === selected.id} onClick={() => void watchLive(selected)}>{liveStarting === selected.id ? 'Starting live TV…' : 'Watch Live'}</button>}
            <button className="subtle record-button" disabled={scheduled.has(selected.id)} onClick={() => void record(selected)}><span className="record-icon" />{scheduled.has(selected.id) ? 'Scheduled' : 'Record this program'}</button>
          </div>
        </aside>
      </div>}

      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} onPasswordChanged={() => setToast({ tone: 'success', message: 'Password changed. Other browsers have been signed out.' })} />}

      {playing && <Suspense fallback={null}><HlsPlayer src={playing.src} playlistPath={playing.playlistPath} title={playing.title} startAt={playing.startAt} liveSessionId={playing.liveSessionId} onCastingChange={setCasting} onProgress={playing.recordingId ? (seconds) => rememberPosition(playing.recordingId!, seconds) : undefined} onClose={closePlayer} /></Suspense>}
      {toast && <div className={`toast ${toast.tone}`} role="status">{toast.message}</div>}
    </div>
  )
}
