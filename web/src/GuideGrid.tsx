import type { Channel, Program } from './types'

const MINUTE_WIDTH = 3.5

type Props = {
  channels: Channel[]
  programs: Program[]
  from: Date
  to: Date
  scheduled: Set<string>
  favorites: Set<string>
  selected: Program | null
  searchResultIDs: Set<string>
  activeSearchResultID: string | null
  searchText: string
  onSelect: (program: Program) => void
  onToggleFavorite: (channelID: string) => void
}

function minutesBetween(a: Date, b: Date) { return (a.getTime() - b.getTime()) / 60000 }
function formatTime(date: Date) { return new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(date) }

function highlightText(text: string, query: string) {
  const needle = query.trim()
  if (!needle) return text
  const escaped = needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return text.split(new RegExp(`(${escaped})`, 'ig')).map((part, index) => part.toLowerCase() === needle.toLowerCase() ? <mark key={index}>{part}</mark> : part)
}

export function GuideGrid({ channels, programs, from, to, scheduled, favorites, selected, searchResultIDs, activeSearchResultID, searchText, onSelect, onToggleFavorite }: Props) {
  const totalMinutes = minutesBetween(to, from)
  const ticks = Array.from({ length: Math.ceil(totalMinutes / 30) + 1 }, (_, index) => new Date(from.getTime() + index * 30 * 60000))
  const nowOffset = minutesBetween(new Date(), from) * MINUTE_WIDTH

  return (
    <div className="guide-scroll" aria-label="Program guide">
      <div className="guide-grid" style={{ '--timeline-width': `${totalMinutes * MINUTE_WIDTH}px` } as React.CSSProperties}>
        <div className="channel-corner">Channel</div>
        <div className="timeline-head">
          {ticks.map((tick) => <span key={tick.toISOString()} style={{ left: minutesBetween(tick, from) * MINUTE_WIDTH }}>{formatTime(tick)}</span>)}
        </div>
        {channels.map((channel) => {
          const rowPrograms = programs.filter((program) => program.channelId === channel.id)
          const favorite = favorites.has(channel.id)
          return (
            <div className="channel-row" key={channel.id}>
              <div className="channel-label">
                {channel.logoUrl && <img className="channel-logo" src={channel.logoUrl} alt={`${channel.name} logo`} onError={(event) => { event.currentTarget.hidden = true }} />}
                <div className="channel-number"><button className={`favorite-toggle ${favorite ? 'selected' : ''}`} onClick={() => onToggleFavorite(channel.id)} aria-pressed={favorite} aria-label={`${favorite ? 'Remove' : 'Add'} ${channel.number} ${channel.name} ${favorite ? 'from' : 'to'} favorites`}>★</button><strong>{channel.number}</strong></div>
                <span>{channel.name}</span>
              </div>
              <div className="program-row">
                {ticks.slice(0, -1).map((tick) => <i className="gridline" key={tick.toISOString()} style={{ left: minutesBetween(tick, from) * MINUTE_WIDTH }} />)}
                {nowOffset >= 0 && nowOffset <= totalMinutes * MINUTE_WIDTH && <i className="now-line" style={{ left: nowOffset }} />}
                {rowPrograms.map((program) => {
                  const visibleStart = Math.max(new Date(program.start).getTime(), from.getTime())
                  const visibleEnd = Math.min(new Date(program.end).getTime(), to.getTime())
                  const width = Math.max(44, ((visibleEnd - visibleStart) / 60000) * MINUTE_WIDTH - 4)
                  const isSearchResult = searchResultIDs.has(program.id)
                  return (
                    <button
                      className={`program ${scheduled.has(program.id) ? 'scheduled' : ''} ${selected?.id === program.id ? 'selected' : ''} ${isSearchResult ? 'search-match' : ''} ${activeSearchResultID === program.id ? 'active-search-match' : ''}`}
                      key={program.id}
                      style={{ left: ((visibleStart - from.getTime()) / 60000) * MINUTE_WIDTH + 2, width }}
                      onClick={() => onSelect(program)}
                    >
                      <strong>{isSearchResult ? highlightText(program.title, searchText) : program.title}</strong>
                      <span>{formatTime(new Date(program.start))}{program.subtitle && <> · {isSearchResult ? highlightText(program.subtitle, searchText) : program.subtitle}</>}</span>
                      {scheduled.has(program.id) && <b className="recording-dot" aria-label="Scheduled to record" />}
                    </button>
                  )
                })}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
