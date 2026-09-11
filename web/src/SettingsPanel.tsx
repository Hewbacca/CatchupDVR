import { useState } from 'react'
import { changePassword } from './api'

type Props = { onClose: () => void; onPasswordChanged: () => void }

export function SettingsPanel({ onClose, onPasswordChanged }: Props) {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [message, setMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setMessage('')
    if (newPassword !== confirmation) {
      setMessage('New passwords do not match')
      return
    }
    setSubmitting(true)
    try {
      await changePassword(currentPassword, newPassword)
      onPasswordChanged()
      onClose()
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not change password')
    } finally {
      setSubmitting(false)
    }
  }

  return <div className="settings-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <aside className="settings-panel" aria-label="Settings">
      <button className="icon-button settings-close" onClick={onClose} aria-label="Close settings">×</button>
      <span className="eyebrow">Settings</span>
      <h2>Security</h2>
      <section className="settings-section" aria-labelledby="change-password-title">
        <h3 id="change-password-title">Change password</h3>
        <p>Changing your password signs out every other browser.</p>
        <form onSubmit={(event) => void submit(event)}>
          <label>Current password<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label>
          <label>New password<input type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required minLength={10} /></label>
          <label>Confirm new password<input type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required minLength={10} /></label>
          {message && <p className="settings-error" role="alert">{message}</p>}
          <button className="primary" disabled={submitting}>{submitting ? 'Saving…' : 'Change password'}</button>
        </form>
      </section>
    </aside>
  </div>
}
