import { type FormEvent, useState } from 'react'
import { configureTuner, testTunerConnection } from './api'

type Props = { onConfigured: () => void }
type ConnectionState = 'idle' | 'testing' | 'success' | 'error'

export function TunerSetupScreen({ onConfigured }: Props) {
  const [address, setAddress] = useState('hdhomerun.local')
  const [connection, setConnection] = useState<ConnectionState>('idle')
  const [message, setMessage] = useState('')
  const [finishing, setFinishing] = useState(false)

  async function testConnection() {
    setConnection('testing')
    setMessage('Contacting your HDHomeRun…')
    try {
      const result = await testTunerConnection(address)
      setAddress(result.address)
      setConnection('success')
      setMessage('Connection verified. Your HDHomeRun is ready.')
    } catch (error) {
      setConnection('error')
      setMessage(error instanceof Error ? error.message : 'Could not reach that HDHomeRun.')
    }
  }

  async function finish(event: FormEvent) {
    event.preventDefault()
    setFinishing(true)
    setConnection('testing')
    setMessage('Verifying your HDHomeRun…')
    try {
      const result = await configureTuner(address)
      setAddress(result.address)
      setConnection('success')
      setMessage('Connection verified. Loading your guide…')
      onConfigured()
    } catch (error) {
      setConnection('error')
      setMessage(error instanceof Error ? error.message : 'Could not reach that HDHomeRun.')
    } finally {
      setFinishing(false)
    }
  }

  return <main className="auth-page">
    <section className="auth-card tuner-setup-card">
      <img src="/icon.svg" alt="" className="auth-icon" />
      <span className="eyebrow">One more step</span>
      <h1>Connect your HDHomeRun</h1>
      <p>Enter its IP address, or keep <code>hdhomerun.local</code> if your network supports it.</p>
      <form onSubmit={(event) => void finish(event)}>
        <label>HDHomeRun address<input autoComplete="off" autoCapitalize="none" spellCheck="false" value={address} onChange={(event) => { setAddress(event.target.value); setConnection('idle'); setMessage('') }} placeholder="hdhomerun.local" required /></label>
        <button type="button" className="subtle tuner-test" disabled={connection === 'testing' || finishing} onClick={() => void testConnection()}>{connection === 'testing' && !finishing ? 'Testing…' : 'Test connection'}</button>
        {connection !== 'idle' && <p className={`tuner-connection ${connection}`} role="status"><span aria-hidden="true" />{message}</p>}
        <button className="primary auth-submit" disabled={finishing}>{finishing ? 'Finishing…' : 'Finish setup'}</button>
      </form>
      <small>Finish setup verifies the connection again before saving it to CatchUp’s local database.</small>
    </section>
  </main>
}
