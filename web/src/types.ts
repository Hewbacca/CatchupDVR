export type Channel = { id: string; number: string; name: string; logoUrl?: string }

export type Program = {
  id: string
  channelId: string
  channel: Channel
  start: string
  end: string
  title: string
  subtitle?: string
  description?: string
  category?: string
}

export type Guide = {
  from: string
  to: string
  channels: Channel[]
  programs: Program[]
}

export type Recording = {
  id: number
  programId: string
  channelId: string
  channelNumber: string
  title: string
  programStart: string
  programEnd: string
  scheduledStart: string
  scheduledEnd: string
  status: 'scheduled' | 'recording' | 'completed' | 'failed' | 'cancelled'
  playlistPath?: string
  processId?: number
  startedAt?: string
  finishedAt?: string
  heartbeatAt?: string
  errorMessage?: string
  createdAt: string
}

export type Diagnostics = {
  database: string
  tunerCount: number
  tunersInUse: number
  tunersAvailable: number
  hdHomeRunConfigured: boolean
  recordingsDir: string
  gpuMode: string
  renderDevice: string
  gpuWarning?: string
  recordingEngine: string
}

export type LiveSession = {
  id: string
  channelNumber: string
  title: string
  playlistPath: string
}
