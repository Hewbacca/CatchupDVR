const STORAGE_KEY = 'catchup-resume-v1'

export type ResumePositions = Record<string, number>

export function readResumePositions(): ResumePositions {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, number] => typeof entry[1] === 'number' && Number.isFinite(entry[1]) && entry[1] >= 0))
  } catch {
    return {}
  }
}

export function writeResumePositions(positions: ResumePositions) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(positions)) } catch { /* Playback should still work when storage is unavailable. */ }
}
