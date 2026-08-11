import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import QuickAliasPage from './QuickAliasPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary } from '../api/types'

const activeAccount: AccountSummary = {
  id: 'acc_active',
  name: '主账号',
  real_email: 'owner@example.com',
  icloud_email: 'owner@icloud.com',
  host: 'icloud.com',
  status: 'active',
  alias_total: 3,
  alias_active: 3,
  has_cookies: true,
  has_app_password: true,
  has_proxy: false,
  last_validated: '2026-08-09T09:00:00Z',
  created_at: '2026-08-09T08:00:00Z',
}

const stoppedTask = {
  account_id: 'acc_active',
  label_prefix: '',
  running: false,
  state: 'stopped',
  created: 0,
  attempts: 0,
  failure_count: 0,
  consecutive_failures: 0,
  cooldown_seconds: 0,
  aliases: [],
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <QuickAliasPage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('QuickAliasPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('没有可用账号时引导接入已有账号', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: [] })),
    )
    renderPage()
    expect(await screen.findByText('还没有可用账号')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /接入已有账号/ })).toHaveAttribute(
      'href',
      '/accounts',
    )
  })

  it('一键生成 20 个随机邮箱并复制邮箱---取件 URL 列表', async () => {
    let requestBody: { account_id?: string; count?: number; label_prefix?: string } = {}
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json({ success: true, data: [activeAccount] }),
      ),
	  http.get('/api/generation-task', () =>
		HttpResponse.json({ success: true, data: stoppedTask }),
	  ),
      http.post('/api/create-batch', async ({ request }) => {
        requestBody = (await request.json()) as typeof requestBody
        return HttpResponse.json({
          success: true,
          data: {
            requested: 20,
            created: 20,
            complete: true,
            aliases: Array.from({ length: 20 }, (_, index) => ({
              email: `random-${index + 1}@icloud.com`,
              label: `${requestBody.label_prefix}-${String(index + 1).padStart(2, '0')}`,
              created_at: '2026-08-09T10:00:00Z',
              account_id: requestBody.account_id,
              inbox_url: `/mail/public-token-${index + 1}`,
            })),
          },
        })
      }),
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '生成 20 个邮箱' }))

    expect(await screen.findByText('已生成 20/20 个')).toBeInTheDocument()
    expect(requestBody.account_id).toBe('acc_active')
    expect(requestBody.count).toBe(20)
    expect(requestBody.label_prefix).toMatch(/^随机邮箱-\d{14}-[0-9a-f]{6}$/)
    expect(screen.getByRole('link', { name: /打开第一个取件页/ })).toHaveAttribute(
      'href',
      '/mail/public-token-1',
    )
    await user.click(screen.getByRole('button', { name: '复制全部' }))
    const copied = await navigator.clipboard.readText()
    expect(copied.split('\n')).toHaveLength(20)
    expect(copied.split('\n')[0]).toBe(
      `random-1@icloud.com---${window.location.origin}/mail/public-token-1`,
    )
  })

  it('创建失败时保留页面并显示上游错误', async () => {
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json({ success: true, data: [activeAccount] }),
      ),
	  http.get('/api/generation-task', () =>
		HttpResponse.json({ success: true, data: stoppedTask }),
	  ),
      http.post('/api/create-batch', () =>
        HttpResponse.json(
          { success: false, code: 'UPSTREAM_FAILURE', message: '创建邮箱失败' },
          { status: 502 },
        ),
      ),
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '生成 20 个邮箱' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('创建邮箱失败')
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '生成 20 个邮箱' })).toBeEnabled()
    })
  })

  it('启动并停止服务器持续生成任务', async () => {
	let starts = 0
	let stops = 0
	server.use(
	  http.get('/api/accounts', () =>
		HttpResponse.json({ success: true, data: [activeAccount] }),
	  ),
	  http.get('/api/generation-task', () =>
		HttpResponse.json({ success: true, data: stoppedTask }),
	  ),
	  http.post('/api/generation-task/start', async ({ request }) => {
		starts += 1
		const body = await request.json() as { account_id: string; label_prefix: string }
		return HttpResponse.json({
		  success: true,
		  data: {
			...stoppedTask,
			account_id: body.account_id,
			label_prefix: body.label_prefix,
			running: true,
			state: 'running',
			attempts: 1,
			message: '正在请求创建邮箱',
		  },
		})
	  }),
	  http.post('/api/generation-task/stop', () => {
		stops += 1
		return HttpResponse.json({
		  success: true,
		  data: { ...stoppedTask, message: '任务已停止' },
		})
	  }),
	)
	renderPage()
	const user = userEvent.setup()
	await user.click(await screen.findByRole('button', { name: '开始持续生成' }))
	expect(await screen.findByText('正在请求')).toBeInTheDocument()
	expect(starts).toBe(1)
	await user.click(screen.getByRole('button', { name: '停止任务' }))
	await waitFor(() => expect(screen.getByText('未启动')).toBeInTheDocument())
	expect(stops).toBe(1)
  })
})
