import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, CreatedAlias, GenerationTaskStatus } from '../api/types'
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

interface BatchCreateResult {
  requested: number
  created: number
  complete: boolean
  message?: string
  aliases: CreatedAlias[]
}

function taskStateText(task: GenerationTaskStatus | null): string {
  if (!task || task.state === 'stopped') return '未启动'
  if (task.state === 'stopping') return '正在停止'
  if (task.state === 'running') return '正在请求'
  if (task.state === 'cooldown') return task.consecutive_failures > 0 ? '失败冷却中' : '等待下一次'
  return task.state
}

function nextRunText(raw?: string): string {
  if (!raw) return '—'
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
	const [task, setTask] = useState<GenerationTaskStatus | null>(null)
	const [taskBusy, setTaskBusy] = useState(false)
	const [taskError, setTaskError] = useState('')
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

  useEffect(() => {
    if (!accountId) {
      setTask(null)
      return
    }
    let cancelled = false
    const loadTask = () => {
      request<GenerationTaskStatus>(
        `/api/generation-task?account_id=${encodeURIComponent(accountId)}`,
      )
        .then((data) => {
          if (cancelled) return
          setTask(data)
          setTaskError('')
        })
        .catch((err) => {
          if (cancelled) return
          setTaskError(err instanceof ApiError ? err.message : '任务状态读取失败')
        })
    }
    loadTask()
    const timer = window.setInterval(loadTask, 3_000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [accountId])

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

  async function startContinuousTask() {
    if (!accountId || taskBusy) return
    setTaskBusy(true)
    setTaskError('')
    try {
      const data = await request<GenerationTaskStatus>('/api/generation-task/start', {
        method: 'POST',
        body: JSON.stringify({ account_id: accountId, label_prefix: randomLabel() }),
      })
      setTask(data)
      show('持续生成任务已启动')
    } catch (err) {
      setTaskError(err instanceof ApiError ? err.message : '任务启动失败')
    } finally {
      setTaskBusy(false)
    }
  }

  async function stopContinuousTask() {
    if (!accountId || taskBusy) return
    setTaskBusy(true)
    setTaskError('')
    try {
      const data = await request<GenerationTaskStatus>('/api/generation-task/stop', {
        method: 'POST',
        body: JSON.stringify({ account_id: accountId }),
      })
      setTask(data)
      show('持续生成任务正在停止')
    } catch (err) {
      setTaskError(err instanceof ApiError ? err.message : '任务停止失败')
    } finally {
      setTaskBusy(false)
    }
  }

  const taskLines = (task?.aliases ?? []).map((item) =>
    item.inbox_url
      ? `${item.email}---${new URL(item.inbox_url, window.location.origin).href}`
      : item.email,
  )

  async function copyTaskLines() {
    if (taskLines.length === 0) return
    try {
      await navigator.clipboard.writeText(taskLines.join('\n'))
      show(`已复制最近 ${taskLines.length} 个持续生成邮箱`)
    } catch {
      show('复制失败，请在列表中手动复制')
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
        <>
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
					setTask(null)
					setTaskError('')
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

		  <div className="continuous-task-card" aria-live="polite">
			<div className="continuous-task-heading">
			  <div>
				<span className={`badge ${task?.running ? 'badge-active' : 'badge-neutral'}`}>
				  {taskStateText(task)}
				</span>
				<h3>持续生成任务</h3>
				<p>服务器逐个请求；触发上游限制后自动延长冷却，页面关闭后继续运行。</p>
			  </div>
			  <div className="continuous-task-actions">
				<button
				  type="button"
				  className="primary"
				  onClick={() => void startContinuousTask()}
				  disabled={taskBusy || Boolean(task?.running) || task?.state === 'stopping'}
				>
				  <IconPlus size={16} />
				  {taskBusy && !task?.running ? '启动中…' : '开始持续生成'}
				</button>
				<button
				  type="button"
				  onClick={() => void stopContinuousTask()}
				  disabled={taskBusy || (!task?.running && task?.state !== 'stopping')}
				>
				  停止任务
				</button>
			  </div>
			</div>

			<div className="continuous-task-stats">
			  <div><span>已生成</span><strong>{task?.created ?? 0}</strong></div>
			  <div><span>请求次数</span><strong>{task?.attempts ?? 0}</strong></div>
			  <div><span>连续失败</span><strong>{task?.consecutive_failures ?? 0}</strong></div>
			  <div><span>下次请求</span><strong>{nextRunText(task?.next_run_at)}</strong></div>
			</div>

			{task?.message && <p className="continuous-task-message">{task.message}</p>}
			{taskError && <div className="alert-error" role="alert">{taskError}</div>}

			{taskLines.length > 0 && (
			  <div className="continuous-task-results">
				<div>
				  <strong>最近生成记录</strong>
				  <button type="button" onClick={() => void copyTaskLines()}>
					<IconCopy size={15} />
					复制全部
				  </button>
				</div>
				<textarea
				  className="generated-mailbox-lines"
				  value={taskLines.join('\n')}
				  readOnly
				  rows={Math.min(12, Math.max(4, taskLines.length))}
				  aria-label="持续生成邮箱和取件链接列表"
				/>
			  </div>
			)}
		  </div>
		</>
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
