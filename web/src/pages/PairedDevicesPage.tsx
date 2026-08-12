import { useCallback, useEffect, useState } from 'react'
import { api, RequestError } from '../api'
import type { PairedDevice, Pairing } from '../types'
import { Empty, PageHeader } from '../components/Common'

/**
 * The paired-devices whitelist.
 *
 * The QR code is a bearer secret: anyone who photographs this screen holds it.
 * So scanning must not be enough to get in. The phone shows a six digit code
 * that has to be typed here, which means completing a pairing requires control
 * of both screens — and the adjudication happens on the Mac, where the
 * whitelist lives.
 */
export function PairedDevicesPage() {
  const [devices, setDevices] = useState<PairedDevice[] | null>(null)
  const [mobileEnabled, setMobileEnabled] = useState(true)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const list = await api.pairedDevices()
      setDevices(list.devices)
      setMobileEnabled(list.mobile_enabled)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [])

  useEffect(() => { void refresh() }, [refresh])

  return <>
    <PageHeader
      eyebrow="Paired devices"
      title="已配对设备"
      description="可以用手机浏览器打开控制面的设备。它们只能查看，另外可以切设备出口、切 Selector、跑连通性检测；网关启停、网络配置和订阅导入都不开放。撤销后该设备下一个请求就会失效。"
    />

    {error && <div className="notice warn">{error}</div>}

    {!mobileEnabled
      ? <div className="notice warn">
          这台 Mac 上的控制面目前只监听回环地址，手机还连不上。
          需要在 Control Service 启动参数里加上 <code>--mobile-interface &lt;接口名&gt;</code>（例如 <code>en0</code>）后重启它。
        </div>
      : <PairingPanel onPaired={refresh} />}

    <section className="section">
      <h2>设备列表</h2>
      {devices === null
        ? <Empty text="正在读取已配对设备…" />
        : devices.length === 0
          ? <Empty text="还没有配对过任何设备" />
          : <div className="paired-list">
              {devices.map(device => <PairedRow key={device.id} device={device} onRevoked={refresh} />)}
            </div>}
    </section>
  </>
}

function PairedRow({ device, onRevoked }: { device: PairedDevice; onRevoked: () => void }) {
  const [busy, setBusy] = useState(false)
  const revoke = async () => {
    if (!window.confirm(`撤销「${device.name}」？这台设备会立即失去访问权限，需要重新配对才能再次使用。`)) return
    setBusy(true)
    try {
      await api.revokePairedDevice(device.id)
      onRevoked()
    } finally {
      setBusy(false)
    }
  }
  return <div className="paired-row">
    <span className="paired-identity">
      <strong>{device.name}</strong>
      <small>配对于 {formatTime(device.created_at)}</small>
    </span>
    <span className="paired-seen">
      {device.last_seen_at ? <>最近活动 {formatTime(device.last_seen_at)}{device.last_ip && <> · <code>{device.last_ip}</code></>}</> : '尚未连接过'}
    </span>
    <button type="button" className="ghost-action" onClick={() => void revoke()} disabled={busy}>
      {busy ? '撤销中…' : '撤销'}
    </button>
  </div>
}

function PairingPanel({ onPaired }: { onPaired: () => void }) {
  const [pairing, setPairing] = useState<Pairing | null>(null)
  const [state, setState] = useState<Pairing['state']>('pending')
  const [attemptsLeft, setAttemptsLeft] = useState(5)
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const start = async () => {
    setError(''); setCode(''); setName(''); setState('pending')
    try {
      const created = await api.startPairing()
      setPairing(created)
      setAttemptsLeft(created.attempts_left)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const cancel = async () => {
    if (pairing) await api.cancelPairing(pairing.id).catch(() => {})
    setPairing(null); setError('')
  }

  // Poll so the panel can tell the operator when the phone has actually
  // scanned. The code itself is never sent here — it only exists on the phone.
  useEffect(() => {
    if (!pairing || state === 'completed') return
    let cancelled = false
    const tick = async () => {
      try {
        const status = await api.pairingStatus(pairing.id)
        if (cancelled) return
        setState(status.state)
        setAttemptsLeft(status.attempts_left)
      } catch {
        if (!cancelled) { setPairing(null); setError('配对已过期或被取消，请重新开始。') }
      }
    }
    const timer = setInterval(() => void tick(), 1500)
    return () => { cancelled = true; clearInterval(timer) }
  }, [pairing, state])

  const confirm = async () => {
    if (!pairing) return
    setBusy(true); setError('')
    try {
      await api.confirmPairing(pairing.id, code.trim(), name.trim())
      setState('completed')
      setPairing(null)
      onPaired()
    } catch (cause) {
      if (cause instanceof RequestError) {
        setError(cause.message)
        if (cause.code === 'pairing_abandoned') setPairing(null)
      } else {
        setError(String(cause))
      }
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  if (!pairing) {
    return <section className="section">
      <h2>配对新设备</h2>
      <p className="pair-lead">开始后会生成一个二维码，用手机扫码，然后把手机上显示的 6 位配对码输入到这里完成绑定。</p>
      {error && <div className="notice warn">{error}</div>}
      <button type="button" className="primary" onClick={() => void start()}>配对新设备</button>
    </section>
  }

  return <section className="section pair-panel">
    <h2>配对新设备</h2>
    <div className="pair-grid">
      <div className="pair-qr-side">
        <QRImage value={pairing.url} />
        <p className="pair-step">1 · 用手机扫这个二维码</p>
        <p className="pair-hint">手机需要和这台 Mac 在同一个局域网。二维码 5 分钟内有效。</p>
      </div>
      <div className="pair-form-side">
        <p className={state === 'claimed' ? 'pair-state claimed' : 'pair-state'}>
          {state === 'claimed' ? '手机已扫码，屏幕上应该显示了 6 位数字' : '等待手机扫码…'}
        </p>
        <label>
          <span>2 · 输入手机上显示的配对码</span>
          <input
            value={code}
            onChange={event => setCode(event.target.value.replace(/\D/g, '').slice(0, 6))}
            inputMode="numeric"
            autoComplete="off"
            placeholder="000000"
            className="pair-code-input"
            disabled={state !== 'claimed'}
          />
        </label>
        <label>
          <span>3 · 给这台设备起个名字</span>
          <input
            value={name}
            onChange={event => setName(event.target.value)}
            placeholder="例如：我的 iPhone"
            maxLength={40}
            disabled={state !== 'claimed'}
          />
        </label>
        {error && <div className="notice warn">{error}</div>}
        {attemptsLeft < 5 && <p className="pair-hint">还可以尝试 {attemptsLeft} 次，用完这次配对就会作废。</p>}
        <div className="pair-actions">
          <button type="button" className="primary" onClick={() => void confirm()} disabled={busy || state !== 'claimed' || code.length !== 6}>
            {busy ? '绑定中…' : '完成绑定'}
          </button>
          <button type="button" className="ghost-action" onClick={() => void cancel()}>取消</button>
        </div>
      </div>
    </div>
  </section>
}

function QRImage({ value }: { value: string }) {
  const [src, setSrc] = useState('')
  // The encoder is loaded on demand. It is only needed while a pairing is in
  // progress, and keeping it out of the eager module graph stops every other
  // page — and every test that renders the shell — from paying for it.
  useEffect(() => {
    let cancelled = false
    void import('qrcode')
      .then(module => module.default.toDataURL(value, { margin: 1, width: 320, errorCorrectionLevel: 'M' }))
      .then(dataURL => { if (!cancelled) setSrc(dataURL) })
      .catch(() => { if (!cancelled) setSrc('') })
    return () => { cancelled = true }
  }, [value])
  if (!src) {
    // Falling back to the raw URL keeps pairing possible even if rendering
    // fails; the pair code is what actually gates the binding.
    return <div className="pair-qr-fallback"><code>{value}</code></div>
  }
  return <img className="pair-qr" src={src} alt="配对二维码" width={320} height={320} />
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}
