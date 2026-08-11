import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, Alias, AliasExportResult } from '../api/types'
import AsyncState from '../components/AsyncState'
import CreateAliasDialog from '../components/CreateAliasDialog'
import ConfirmDialog from '../components/ConfirmDialog'
import { useToast } from '../components/ToastProvider'
import { IconCheck, IconClock, IconCopy, IconDownload, IconPlus, IconSearch, IconTrash } from '../components/icons'

function formatDate(raw: string): string {
  const numeric = /^\d{10,13}$/.test(raw) ? Number(raw) : Number.NaN
  const d = Number.isNaN(numeric)
    ? new Date(raw)
    : new Date(raw.length === 10 ? numeric * 1000 : numeric)
  if (Number.isNaN(d.getTime())) return raw
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

export default function AliasesPage() {
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [accountId, setAccountId] = useState('')
  const [aliases, setAliases] = useState<Alias[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [exportFilter, setExportFilter] = useState<'all' | 'pending' | 'exported'>('all')
  const [createOpen, setCreateOpen] = useState(false)
  const [confirm, setConfirm] = useState<{
    type: 'deactivate' | 'reactivate' | 'delete'
    alias: Alias
  } | null>(null)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const [exporting, setExporting] = useState(false)
  const [selectedEmails, setSelectedEmails] = useState<Set<string>>(new Set())
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const pageSelectionRef = useRef<HTMLInputElement>(null)

  const [searchParams, setSearchParams] = useSearchParams()
  const { show } = useToast()

  // 加载账号列表
  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        const queryId = searchParams.get('account_id')
        const valid = data.find((a) => a.id === queryId)
        const target = valid ? valid.id : data[0]?.id ?? ''
        setAccountId(target)
        if (target && (!queryId || !valid)) {
          setSearchParams({ account_id: target }, { replace: true })
        }
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 加载别名列表
  useEffect(() => {
    if (!accountId) return
    let cancelled = false
    request<{ account_id: string; count: number; aliases: Alias[] }>(
      `/api/aliases?account_id=${encodeURIComponent(accountId)}`,
    )
      .then((data) => {
        if (cancelled) return
        setAliases(data.aliases ?? [])
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
  }, [accountId, retryKey])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return aliases.filter((a) => {
      if (filter === 'active' && !a.active) return false
      if (filter === 'inactive' && a.active) return false
      if (exportFilter === 'pending' && a.exported) return false
      if (exportFilter === 'exported' && !a.exported) return false
      if (!q) return true
      return (
        a.email.toLowerCase().includes(q) || a.label.toLowerCase().includes(q)
      )
    })
  }, [aliases, search, filter, exportFilter])

  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const pagedAliases = useMemo(
    () => filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize),
    [currentPage, filtered, pageSize],
  )
  const selectedAliases = useMemo(
    () => filtered.filter(
      (alias) => alias.inboxUrl && !alias.exported && selectedEmails.has(alias.email.toLowerCase()),
    ),
    [filtered, selectedEmails],
  )
  const selectableOnPage = useMemo(
    () => pagedAliases.filter((alias) => alias.inboxUrl && !alias.exported),
    [pagedAliases],
  )
  const selectedOnPage = selectableOnPage.filter((alias) => selectedEmails.has(alias.email.toLowerCase())).length
  const allOnPageSelected = selectableOnPage.length > 0 && selectedOnPage === selectableOnPage.length

  useEffect(() => {
    if (pageSelectionRef.current) {
      pageSelectionRef.current.indeterminate = selectedOnPage > 0 && !allOnPageSelected
    }
  }, [allOnPageSelected, selectedOnPage])

  function handleRetry() {
    setLoading(true)
    setRetryKey((k) => k + 1)
  }

  const copyEmail = useCallback(
    async (email: string) => {
      try {
        await navigator.clipboard.writeText(email)
        show('邮箱已复制')
      } catch {
        show(`复制失败，请手动复制：${email}`)
      }
    },
    [show],
  )

  const copyInboxURL = useCallback(
    async (email: string, path: string) => {
      const url = new URL(path, window.location.origin).href
      const line = `${email}---${url}`
      try {
        await navigator.clipboard.writeText(line)
        show('邮箱和取件链接已复制')
      } catch {
        show(`复制失败，请手动复制：${line}`)
      }
    },
    [show],
  )

  const exportSelectedAliases = useCallback(async () => {
    if (selectedAliases.length === 0 || exporting) return
    setExporting(true)
    setActionError('')
    try {
      const data = await request<AliasExportResult>('/api/aliases/export', {
        method: 'POST',
        body: {
          account_id: accountId,
          emails: selectedAliases.map((alias) => alias.email),
        },
      })
      if (data.items.length === 0) {
        setRetryKey((key) => key + 1)
        show('这些邮箱此前已经导出')
        return
      }

      const lines = data.items.map(
        (item) => `${item.email}---${new URL(item.inbox_url, window.location.origin).href}`,
      )
      const blob = new Blob([`\uFEFF${lines.join('\n')}\n`], { type: 'text/plain;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `icloud-aliases-${new Date().toISOString().slice(0, 10)}.txt`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)

      const exported = new Map(data.items.map((item) => [item.email.toLowerCase(), item.exported_at]))
      setAliases((current) => current.map((alias) => {
        const exportedAt = exported.get(alias.email.toLowerCase())
        return exportedAt ? { ...alias, exported: true, exportedAt } : alias
      }))
      setSelectedEmails((current) => {
        const next = new Set(current)
        data.items.forEach((item) => next.delete(item.email.toLowerCase()))
        return next
      })
      show(`已导出 ${data.items.length} 个邮箱，本次不会包含已导出项`)
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : '导出失败，请检查服务状态')
    } finally {
      setExporting(false)
    }
  }, [accountId, exporting, selectedAliases, show])

  function toggleAlias(email: string, checked: boolean) {
    const key = email.toLowerCase()
    setSelectedEmails((current) => {
      const next = new Set(current)
      if (checked) next.add(key)
      else next.delete(key)
      return next
    })
  }

  function toggleCurrentPage() {
    setSelectedEmails((current) => {
      const next = new Set(current)
      selectableOnPage.forEach((alias) => {
        const key = alias.email.toLowerCase()
        if (allOnPageSelected) next.delete(key)
        else next.add(key)
      })
      return next
    })
  }

  async function runAction(type: 'deactivate' | 'reactivate' | 'delete') {
    if (!confirm) return
    setBusy(true)
    setActionError('')
    const { alias } = confirm
    try {
      if (type === 'delete') {
        await request(
          `/api/aliases/${encodeURIComponent(alias.anonymousId)}`,
          { method: 'DELETE', body: JSON.stringify({ account_id: accountId }) },
        )
        show('别名已删除')
      } else {
        await request(
          `/api/aliases/${encodeURIComponent(alias.anonymousId)}/${type === 'deactivate' ? 'deactivate' : 'reactivate'}`,
          { method: 'POST', body: JSON.stringify({ account_id: accountId }) },
        )
        show(type === 'deactivate' ? '别名已停用' : '别名已激活')
      }
      setConfirm(null)
      setRetryKey((k) => k + 1)
    } catch (err) {
      setActionError(
        err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态',
      )
    } finally {
      setBusy(false)
    }
  }

  function handleCreated(email: string) {
    setCreateOpen(false)
    show(`别名已创建：${email}`)
    setRetryKey((k) => k + 1)
  }

  if (accounts.length === 0 && !loading && !error) {
    return <p className="empty-state">暂无账号，请先到「账号」页面添加账号</p>
  }

  const confirmTitle =
    confirm?.type === 'delete'
      ? '删除别名'
      : confirm?.type === 'deactivate'
        ? '停用别名'
        : '激活别名'

  const confirmLabel =
    confirm?.type === 'delete' ? '确认删除' : confirm?.type === 'deactivate' ? '确认停用' : '确认激活'

  return (
    <section>
      <div className="page-header">
        <div className="page-title">
          <h2>别名管理</h2>
          <p>创建、停用、激活或删除 Hide My Email 别名</p>
        </div>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
          <label htmlFor="alias-account">账号</label>
          <select
            id="alias-account"
            value={accountId}
            onChange={(e) => {
              setAccountId(e.target.value)
              setSelectedEmails(new Set())
              setPage(1)
              setSearchParams({ account_id: e.target.value }, { replace: true })
            }}
            style={{ width: 'auto' }}
          >
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          <button className="primary" onClick={() => setCreateOpen(true)} disabled={!accountId}>
            <IconPlus size={16} />
            创建别名
          </button>
          <button
            type="button"
            onClick={() => void exportSelectedAliases()}
            disabled={selectedAliases.length === 0 || exporting}
          >
            <IconDownload size={16} />
            {exporting ? '导出中' : `导出选中 (${selectedAliases.length})`}
          </button>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 12, marginBottom: 16, flexWrap: 'wrap' }}>
        <div style={{ flex: 1, minWidth: 200 }}>
          <label htmlFor="alias-search">搜索</label>
          <div style={{ position: 'relative' }}>
            <input
              id="alias-search"
              type="search"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                setSelectedEmails(new Set())
                setPage(1)
              }}
              placeholder="按邮箱或标签搜索"
              style={{ paddingRight: 36 }}
            />
            <span
              aria-hidden="true"
              style={{
                position: 'absolute',
                right: 10,
                top: '50%',
                transform: 'translateY(-50%)',
                color: 'var(--color-text-tertiary)',
                display: 'flex',
                pointerEvents: 'none',
              }}
            >
              <IconSearch size={16} />
            </span>
          </div>
        </div>
        <div>
          <label htmlFor="alias-filter">状态</label>
          <select
            id="alias-filter"
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value as 'all' | 'active' | 'inactive')
              setSelectedEmails(new Set())
              setPage(1)
            }}
            style={{ width: 'auto' }}
          >
            <option value="all">全部</option>
            <option value="active">已启用</option>
            <option value="inactive">已停用</option>
          </select>
        </div>
        <div>
          <label htmlFor="alias-export-filter">导出状态</label>
          <select
            id="alias-export-filter"
            value={exportFilter}
            onChange={(e) => {
              setExportFilter(e.target.value as 'all' | 'pending' | 'exported')
              setSelectedEmails(new Set())
              setPage(1)
            }}
            style={{ width: 'auto' }}
          >
            <option value="all">全部</option>
            <option value="pending">未导出</option>
            <option value="exported">已导出</option>
          </select>
        </div>
      </div>

      <AsyncState
        loading={loading}
        error={error}
        empty={filtered.length === 0}
        emptyText={aliases.length === 0 ? '暂无别名' : '没有匹配的别名'}
        onRetry={handleRetry}
      >
        <div className="alias-list">
          <div className="alias-selection-bar" aria-live="polite">
            <span>已选择 <strong>{selectedAliases.length}</strong> 个，可跨页选择</span>
            {selectedAliases.length > 0 && (
              <button type="button" className="ghost" onClick={() => setSelectedEmails(new Set())}>
                清空选择
              </button>
            )}
          </div>
          <div className="table-wrap">
          <table className="alias-table">
            <thead>
              <tr>
                <th className="alias-select-cell">
                  <input
                    ref={pageSelectionRef}
                    className="selection-checkbox"
                    type="checkbox"
                    checked={allOnPageSelected}
                    disabled={selectableOnPage.length === 0}
                    onChange={toggleCurrentPage}
                    aria-label="选择本页可导出邮箱"
                  />
                </th>
                <th>邮箱</th>
                <th>标签</th>
                <th>状态</th>
                <th>导出状态</th>
                <th>创建时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {pagedAliases.map((alias) => {
                const selectable = Boolean(alias.inboxUrl && !alias.exported)
                return (
                <tr key={alias.anonymousId}>
                  <td className="alias-select-cell">
                    <input
                      className="selection-checkbox"
                      type="checkbox"
                      checked={selectedEmails.has(alias.email.toLowerCase())}
                      disabled={!selectable}
                      onChange={(event) => toggleAlias(alias.email, event.target.checked)}
                      aria-label={`选择 ${alias.email}`}
                      title={selectable ? '选择导出' : alias.exported ? '该邮箱已导出' : '该邮箱没有取件链接'}
                    />
                  </td>
                  <td>
                    <button
                      type="button"
                      className="link-like"
                      onClick={() => void copyEmail(alias.email)}
                      title="复制邮箱"
                    >
                      {alias.email}
                    </button>
                  </td>
                  <td>{alias.label || '—'}</td>
                  <td>
                    <span className={alias.active ? 'badge badge-active' : 'badge badge-neutral'}>
                      {alias.active ? <IconCheck size={12} /> : <IconClock size={12} />}
                      {alias.active ? '已启用' : '已停用'}
                    </span>
                  </td>
                  <td>
                    <span className={alias.exported ? 'badge badge-info' : 'badge badge-pending'}>
                      {alias.exported ? <IconCheck size={12} /> : <IconClock size={12} />}
                      {alias.exported ? '已导出' : '未导出'}
                    </span>
                    {alias.exportedAt && <span className="cell-secondary">{formatDate(alias.exportedAt)}</span>}
                  </td>
                  <td>{alias.createdAt ? formatDate(alias.createdAt) : '—'}</td>
                  <td>
                    <div className="row-actions">
                      {alias.active ? (
                        <button
                          disabled={busy}
                          onClick={() => setConfirm({ type: 'deactivate', alias })}
                        >
                          停用
                        </button>
                      ) : (
                        <button
                          disabled={busy}
                          onClick={() => setConfirm({ type: 'reactivate', alias })}
                        >
                          激活
                        </button>
                      )}
                      {alias.inboxUrl && (
                        <>
                          <a href={alias.inboxUrl} target="_blank" rel="noreferrer">
                            取件页
                          </a>
                          <button
                            onClick={() => void copyInboxURL(alias.email, alias.inboxUrl ?? '')}
                            title="复制邮箱和取件链接"
                            aria-label={`复制 ${alias.email} 和取件链接`}
                          >
                            <IconCopy size={14} />
                          </button>
                        </>
                      )}
                      <button
                        className="danger"
                        disabled={busy}
                        onClick={() => setConfirm({ type: 'delete', alias })}
                      >
                        <IconTrash size={14} />
                        删除
                      </button>
                    </div>
                  </td>
                </tr>
                )
              })}
            </tbody>
          </table>
          </div>
          <div className="alias-pagination">
            <div className="alias-pagination-summary">
              共 {filtered.length} 个，第 {(currentPage - 1) * pageSize + 1}–{Math.min(currentPage * pageSize, filtered.length)} 个
            </div>
            <div className="alias-pagination-controls">
              <label htmlFor="alias-page-size">每页</label>
              <select
                id="alias-page-size"
                value={pageSize}
                onChange={(event) => {
                  setPageSize(Number(event.target.value))
                  setPage(1)
                }}
              >
                <option value={10}>10</option>
                <option value={20}>20</option>
                <option value={50}>50</option>
                <option value={100}>100</option>
              </select>
              <button type="button" onClick={() => setPage(currentPage - 1)} disabled={currentPage === 1}>
                上一页
              </button>
              <span>第 {currentPage} / {totalPages} 页</span>
              <button type="button" onClick={() => setPage(currentPage + 1)} disabled={currentPage === totalPages}>
                下一页
              </button>
            </div>
          </div>
        </div>
      </AsyncState>

      <CreateAliasDialog
        accountId={accountId}
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />

      {confirm && (
        <ConfirmDialog
          title={confirmTitle}
          message={
            confirm.type === 'delete'
              ? `将删除别名 ${confirm.alias.email}。此操作不可恢复，且不会影响 Apple 账号本身。`
              : confirm.type === 'deactivate'
                ? `将停用别名 ${confirm.alias.email}，之后该邮箱将不再接收邮件。`
                : `将重新激活别名 ${confirm.alias.email}。`
          }
          confirmLabel={confirmLabel}
          requireText={
            confirm.type === 'delete' ? confirm.alias.email : undefined
          }
          requireLabel={
            confirm.type === 'delete' ? '输入完整邮箱' : undefined
          }
          open
          busy={busy}
          onClose={() => setConfirm(null)}
          onConfirm={() => void runAction(confirm.type)}
        />
      )}

      {actionError && (
        <div className="alert-error" role="alert" style={{ marginTop: 16 }}>
          {actionError}
        </div>
      )}
    </section>
  )
}
