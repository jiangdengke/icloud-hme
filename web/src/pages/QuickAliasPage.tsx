import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary } from '../api/types'
import { useToast } from '../components/ToastProvider'
import {
  IconAccounts,
  IconCheck,
  IconCopy,
  IconInbox,
  IconMail,
  IconPlus,
  IconRefresh,
} from '../components/icons'

interface CreatedAlias {
  email: string
  label: string
  created_at: string
  account_id: string
  inbox_url?: string
}

interface BatchCreateResult {
  requested: number
  created: number
  complete: boolean
  message?: string
  aliases: CreatedAlias[]
}

function randomLabel(): string {
  const bytes = new Uint8Array(3)
  crypto.getRandomValues(bytes)
  const suffix = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  const timestamp = new Date().toISOString().replace(/[-:TZ.]/g, '').slice(0, 14)
  return `随机邮箱-${timestamp}-${suffix}`
}

export default function QuickAliasPage() {
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [accountId, setAccountId] = useState('')
  const [created, setCreated] = useState<CreatedAlias[]>([])
  const [batchResult, setBatchResult] = useState<BatchCreateResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [batchMessage, setBatchMessage] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const { show } = useToast()

  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        const firstReady = data.find((account) => account.status === 'active' && account.has_cookies)
        setAccountId((current) => {
          const stillReady = data.some(
            (account) => account.id === current && account.status === 'active' && account.has_cookies,
          )
          return stillReady ? current : firstReady?.id ?? ''
        })
        setError('')
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  const readyAccounts = useMemo(
    () => accounts.filter((account) => account.status === 'active' && account.has_cookies),
    [accounts],
  )
  const selectedAccount = readyAccounts.find((account) => account.id === accountId)

  async function createRandomAlias() {
    if (!accountId || creating) return
    setCreating(true)
    setError('')
    setBatchMessage('')
    setCreated([])
    setBatchResult(null)
    try {
      const data = await request<BatchCreateResult>('/api/create-batch', {
        method: 'POST',
        body: JSON.stringify({ account_id: accountId, count: 20, label_prefix: randomLabel() }),
      })
      setBatchResult(data)
      setCreated(data.aliases ?? [])
      setBatchMessage(data.complete ? '' : data.message ?? '批量创建中途停止，已保留已生成的邮箱')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setCreating(false)
    }
  }

  const generatedLines = created.map((item) =>
    item.inbox_url
      ? `${item.email}---${new URL(item.inbox_url, window.location.origin).href}`
      : item.email,
  )

  async function copyGeneratedLines() {
    if (generatedLines.length === 0) return
    const text = generatedLines.join('\n')
    try {
      await navigator.clipboard.writeText(text)
      show(`已复制 ${generatedLines.length} 个邮箱和取件链接`)
    } catch {
      show(`复制失败，请手动复制：${text}`)
    }
  }

  return (
    <section>
      <div className="page-header">
        <div className="page-title">
          <h2>生成邮箱</h2>
          <p>一键创建随机隐藏邮箱地址</p>
        </div>
      </div>

      {loading && (
        <div className="skeleton" aria-busy="true" aria-label="加载账号">
          <div className="skeleton-line" style={{ width: '38%' }} />
          <div className="skeleton-line" style={{ width: '72%' }} />
          <div className="skeleton-line" style={{ width: '54%' }} />
        </div>
      )}

      {!loading && readyAccounts.length === 0 && !error && (
        <div className="empty-state quick-alias-empty">
          <IconAccounts className="empty-icon" />
          <strong>还没有可用账号</strong>
          <Link className="button-link primary" to="/accounts">
            <IconPlus size={16} />
            接入已有账号
          </Link>
        </div>
      )}

      {!loading && readyAccounts.length > 0 && (
        <div className="quick-alias-tool">
          <div className="quick-alias-controls">
            <div>
              <span className="badge badge-active">
                <IconCheck size={12} />
                账号可用
              </span>
              <h3>{selectedAccount?.name}</h3>
              <p>{selectedAccount?.icloud_email || selectedAccount?.real_email}</p>
            </div>

            {readyAccounts.length > 1 && (
              <div className="form-field">
                <label htmlFor="quick-alias-account">生成账号</label>
                <select
                  id="quick-alias-account"
                  value={accountId}
                  onChange={(event) => {
                    setAccountId(event.target.value)
                    setCreated([])
                    setBatchResult(null)
                    setBatchMessage('')
                  }}
                >
                  {readyAccounts.map((account) => (
                    <option key={account.id} value={account.id}>
                      {account.name}
                    </option>
                  ))}
                </select>
              </div>
            )}

            <button
              type="button"
              className="primary quick-generate-button"
              onClick={() => void createRandomAlias()}
              disabled={creating}
            >
              {creating ? <IconRefresh size={18} /> : <IconPlus size={18} />}
              {creating ? '正在生成 20 个…' : created.length > 0 ? '再生成 20 个' : '生成 20 个邮箱'}
            </button>
          </div>

          <div className="quick-alias-result" aria-live="polite">
            {created.length > 0 ? (
              <>
                <span className="quick-result-icon" aria-hidden="true">
                  <IconCheck size={24} />
                </span>
                <p className="quick-result-label">
                  已生成 {batchResult?.created ?? created.length}/{batchResult?.requested ?? 20} 个
                </p>
                <textarea
                  className="generated-mailbox-lines"
                  value={generatedLines.join('\n')}
                  readOnly
                  rows={Math.min(12, Math.max(4, generatedLines.length))}
                  aria-label="邮箱和取件链接列表"
                />
                {batchMessage && <p className="quick-batch-message">{batchMessage}</p>}
                <div className="quick-result-actions">
                  <button type="button" onClick={() => void copyGeneratedLines()}>
                    <IconCopy size={16} />
                    复制全部
                  </button>
                  {created[0]?.inbox_url ? (
                    <a className="button-link" href={created[0].inbox_url} target="_blank" rel="noreferrer">
                      <IconInbox size={16} />
                      打开第一个取件页
                    </a>
                  ) : (
                    <Link
                      className="button-link"
                      to={`/inbox?account_id=${encodeURIComponent(created[0].account_id)}&alias=${encodeURIComponent(created[0].email)}`}
                    >
                      <IconInbox size={16} />
                      收件箱
                    </Link>
                  )}
                </div>
              </>
            ) : (
              <>
                <span className="quick-result-icon neutral" aria-hidden="true">
                  <IconMail size={24} />
                </span>
                <p className="quick-result-label">等待生成</p>
              </>
            )}
          </div>
        </div>
      )}

      {error && (
        <div className="alert-error quick-alias-error" role="alert">
          <span>{error}</span>
          <button
            type="button"
            onClick={() => {
              setLoading(true)
              setError('')
              setRetryKey((key) => key + 1)
            }}
          >
            重试
          </button>
        </div>
      )}
    </section>
  )
}
