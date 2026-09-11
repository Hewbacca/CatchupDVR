import { createCastMedia } from './api'

type CastWindow = Window & {
  __onGCastApiAvailable?: (available: boolean) => void
  cast?: any
  chrome?: any
}

let castApi: Promise<any> | undefined
let configured = false

function castWindow() { return window as CastWindow }

function configureCast(context: any) {
  if (!configured) {
    const currentWindow = castWindow()
    context.setOptions({
      receiverApplicationId: currentWindow.chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
      autoJoinPolicy: currentWindow.chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED,
    })
    configured = true
  }
  return context
}

export function getCastContext(): Promise<any> {
  if (castApi) return castApi
  castApi = new Promise((resolve, reject) => {
    const currentWindow = castWindow()
    if (currentWindow.cast?.framework && currentWindow.chrome?.cast) {
      resolve(configureCast(currentWindow.cast.framework.CastContext.getInstance()))
      return
    }

    const previousCallback = currentWindow.__onGCastApiAvailable
    currentWindow.__onGCastApiAvailable = (available: boolean) => {
      previousCallback?.(available)
      if (!available || !currentWindow.cast?.framework || !currentWindow.chrome?.cast) {
        reject(new Error('Google Cast is not available in this browser.'))
        return
      }
      resolve(configureCast(currentWindow.cast.framework.CastContext.getInstance()))
    }

    const existing = document.getElementById('google-cast-sender') as HTMLScriptElement | null
    if (existing) return
    const script = document.createElement('script')
    script.id = 'google-cast-sender'
    script.src = 'https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1'
    script.async = true
    script.onerror = () => reject(new Error('Could not load Google Cast. Check this browser’s connection and privacy settings.'))
    document.head.appendChild(script)
  })
  return castApi
}

export async function castPlaylist(options: { playlistPath: string; liveSessionId?: string; title: string; startAt: number }) {
  if (!window.isSecureContext) throw new Error('Open CatchUp through its HTTPS address before casting.')
  const context = await getCastContext()
  const session = await context.requestSession()
  try {
    const media = await createCastMedia(options.playlistPath, options.liveSessionId)
    const currentWindow = castWindow()
    const mediaInfo = new currentWindow.chrome.cast.media.MediaInfo(new URL(media.path, window.location.origin).toString(), 'application/vnd.apple.mpegurl')
    // HLS EVENT recordings are seekable even while their source is still live.
    mediaInfo.streamType = currentWindow.chrome.cast.media.StreamType.BUFFERED
    const metadata = new currentWindow.chrome.cast.media.GenericMediaMetadata()
    metadata.title = options.title
    mediaInfo.metadata = metadata
    const request = new currentWindow.chrome.cast.media.LoadRequest(mediaInfo)
    request.autoplay = true
    request.currentTime = Math.max(0, options.startAt)
    return await session.loadMedia(request)
  } catch (error) {
    await context.endCurrentSession(true).catch(() => undefined)
    throw error
  }
}
