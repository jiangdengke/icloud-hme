import { useCallback, useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { InboxMessage, InboxResult } from '../api/types'
import { IconCopy, IconInbox, IconRefresh } from '../components/icons'

function formatDate(raw: string): string {
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date)
}

function extractCode(message: InboxMessage): string {
  const text = `${message.subject} ${message.preview}`
  return text.match(/\b\d{6}\b/)?.[0] ?? text.match(/\b\d{4,8}\b/)?.[0] ?? ''
}

export default function PublicInboxPage() {
  const { token = '' } = useParams()
  const [result, setResult] = useState<InboxResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const load = useCallback(async (initial = false) => {
    if (!token) return
    if (initial) setLoading(true)
    else setRefreshing(true)
    try {
      const data = await request<InboxResult>(`/api/public/mail/${encodeURIComponent(token)}`)
      setResult(data)
      setError('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请稍后重试')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [token])

  useEffect(() => {
    void load(true)
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void load(false)
    }, 10_000)
    return () => window.clearInterval(timer)
  }, [load])

  const messages = useMemo(() => result?.messages ?? [], [result])

  async function copyText(value: string, key: string) {
    await navigator.clipboard.writeText(value)
    setCopied(key)
    window.setTimeout(() => setCopied(''), 1500)
  }

  return (
    <main className="public-inbox-page">
      <section className="public-inbox-card">
        <div className="public-inbox-header">
          <span className="public-inbox-icon" aria-hidden="true"><IconInbox size={24} /></span>
          <div>
            <h1>邮箱取件</h1>
            <p>此链接只显示发往当前邮箱的邮件</p>
          </div>
          <button
            type="button"
            onClick={() => void load(false)}
            disabled={refreshing}
            aria-label="刷新邮件"
          >
            <IconRefresh size={17} />
            {refreshing ? '刷新中…' : '刷新'}
          </button>
        </div>

        {result?.alias && (
          <div className="public-inbox-address">
            <code>{result.alias}</code>
            <button type="button" onClick={() => void copyText(result.alias ?? '', 'address')}>
              <IconCopy size={15} />
              {copied === 'address' ? '已复制' : '复制邮箱'}
            </button>
          </div>
        )}

        <p className="public-inbox-refresh-hint">页面每 10 秒自动刷新，也可以手动刷新。</p>

        {loading && <div className="empty-state" aria-busy="true">正在读取邮件…</div>}
        {!loading && error && <div className="alert-error" role="alert">{error}</div>}
        {!loading && !error && messages.length === 0 && (
          <div className="empty-state">
            <IconInbox className="empty-icon" />
            <strong>暂时没有邮件</strong>
            <span>发送后等待几秒，页面会自动刷新。</span>
          </div>
        )}

        {!loading && !error && messages.length > 0 && (
          <div className="public-message-list" aria-live="polite">
            {messages.map((message) => {
              const code = extractCode(message)
              return (
                <article className="public-message" key={message.id}>
                  <div className="public-message-heading">
                    <div>
                      <h2>{message.subject || '（无主题）'}</h2>
                      <p>{message.from}</p>
                    </div>
                    <time>{formatDate(message.date)}</time>
                  </div>
                  {code && (
                    <button
                      type="button"
                      className="public-message-code"
                      onClick={() => void copyText(code, message.id)}
                    >
                      <span>验证码</span>
                      <strong>{code}</strong>
                      <IconCopy size={15} />
                      {copied === message.id ? '已复制' : '复制'}
                    </button>
                  )}
                  <p className="public-message-preview">{message.preview || '（无文本摘要）'}</p>
                  <p className="public-message-recipient">收件人：{message.to}</p>
                </article>
              )
            })}
          </div>
        )}
      </section>
    </main>
  )
}
