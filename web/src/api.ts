import type { Diagnostics, Guide, LiveSession, Program, Recording } from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...init })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`)
  return body as T
}

export type AuthStatus = { setupRequired: boolean; authenticated: boolean; tunerSetupRequired: boolean }

export function getAuthStatus() { return request<AuthStatus>('/api/auth/status') }

export function setupAccount(username: string, password: string) {
  return request<AuthStatus>('/api/auth/setup', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }),
  })
}

export function login(username: string, password: string) {
  return request<AuthStatus>('/api/auth/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }),
  })
}

export type TunerConnection = { connected: boolean; address: string }

export function testTunerConnection(address: string) {
  return request<TunerConnection>('/api/setup/tuner/test', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ address }),
  })
}

export function configureTuner(address: string) {
  return request<{ configured: boolean; address: string }>('/api/setup/tuner', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ address }),
  })
}

export async function logout() {
  const response = await fetch('/api/auth/logout', { method: 'POST', credentials: 'same-origin' })
  if (!response.ok) throw new Error('Could not sign out')
}

export function changePassword(currentPassword: string, newPassword: string) {
  return request<{ changed: boolean }>('/api/auth/password', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ currentPassword, newPassword }),
  })
}

export function getGuide(from: Date, to: Date) {
  const query = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() })
  return request<Guide>(`/api/guide?${query}`)
}

export function getGuideDays() { return request<string[]>('/api/guide/days') }

export function searchGuide(query: string) {
  return request<Program[]>(`/api/guide/search?${new URLSearchParams({ q: query })}`)
}

export function getFavoriteChannels() { return request<string[]>('/api/preferences/favorite-channels') }

export function setFavoriteChannel(channelID: string, favorite: boolean) {
  return request<void>(`/api/preferences/favorite-channels/${encodeURIComponent(channelID)}`, { method: favorite ? 'PUT' : 'DELETE' })
}

export function getRecordings() { return request<Recording[]>('/api/recordings') }
export function getDiagnostics() { return request<Diagnostics>('/api/diagnostics') }

export function startLive(channelNumber: string, title: string) {
  return request<LiveSession>('/api/live', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ channelNumber, title }),
  })
}

export function createCastMedia(playlistPath: string, liveSessionId?: string) {
  return request<{ path: string }>('/api/cast', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ playlistPath, liveSessionId }),
  })
}

export async function stopLive(id: string, keepalive = false) {
  const response = await fetch(`/api/live/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body.error || 'Could not stop live TV')
  }
}

export function schedule(programId: string) {
  return request<Recording>('/api/recordings', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ programId }),
  })
}

export async function removeRecording(id: number) {
  const response = await fetch(`/api/recordings/${id}`, { method: 'DELETE' })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body.error || 'Could not delete recording')
  }
}
