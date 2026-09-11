import { useState } from 'react'
import { login, setupAccount } from './api'

type Props = { setupRequired: boolean; onAuthenticated: () => void }

export function AuthScreen({ setupRequired, onAuthenticated }: Props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    if (setupRequired && password !== confirmation) {
      setError('Passwords do not match')
      return
    }
    setSubmitting(true)
    try {
      if (setupRequired) await setupAccount(username, password)
      else await login(username, password)
      onAuthenticated()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not sign in')
    } finally {
      setSubmitting(false)
    }
  }

  return <main className="auth-page">
    <section className="auth-card" aria-labelledby="auth-title">
      <img src="/icon.svg" alt="" className="auth-icon" />
      <span className="eyebrow">CatchUp DVR</span>
      <h1 id="auth-title">{setupRequired ? 'Create your account' : 'Welcome back'}</h1>
      <p>{setupRequired ? 'This one-time account protects CatchUp before it is available on the internet.' : 'Sign in to view your guide and recordings.'}</p>
      <form onSubmit={(event) => void submit(event)}>
        <label>Username<input autoComplete={setupRequired ? 'username' : 'username'} value={username} onChange={(event) => setUsername(event.target.value)} required maxLength={128} /></label>
        <label>Password<input type="password" autoComplete={setupRequired ? 'new-password' : 'current-password'} value={password} onChange={(event) => setPassword(event.target.value)} required minLength={setupRequired ? 12 : undefined} /></label>
        {setupRequired && <label>Confirm password<input type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required minLength={12} /></label>}
        {error && <p className="auth-error" role="alert">{error}</p>}
        <button className="primary auth-submit" disabled={submitting}>{submitting ? 'Please wait…' : setupRequired ? 'Create account' : 'Sign in'}</button>
      </form>
      {setupRequired && <small>Your password is stored only as a secure hash in CatchUp’s local database.</small>}
    </section>
  </main>
}
