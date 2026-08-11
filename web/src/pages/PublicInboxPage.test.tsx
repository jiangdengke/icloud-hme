import { http, HttpResponse } from 'msw'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import PublicInboxPage from './PublicInboxPage'
import { server } from '../test/server'

describe('PublicInboxPage', () => {
  beforeEach(() => server.resetHandlers())

  it('只通过 token 读取对应别名并显示验证码', async () => {
    let path = ''
    server.use(
      http.get('/api/public/mail/:token', ({ params }) => {
        path = String(params.token)
        return HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            alias: 'alpha@icloud.com',
            count: 1,
            method: 'imap',
            messages: [{
              id: 'm1',
              from: 'GitHub <noreply@github.com>',
              to: 'alpha@icloud.com',
              subject: 'Your GitHub verification code is 123456',
              date: '2026-08-10T10:00:00Z',
              preview: 'Use 123456 to verify your email.',
            }],
          },
        })
      }),
    )
    render(
      <MemoryRouter initialEntries={['/mail/token-alpha']}> 
        <Routes>
          <Route path="/mail/:token" element={<PublicInboxPage />} />
        </Routes>
      </MemoryRouter>,
    )

    expect(await screen.findByText('alpha@icloud.com')).toBeInTheDocument()
    expect(screen.getByText('123456')).toBeInTheDocument()
    expect(screen.getByText('GitHub <noreply@github.com>')).toBeInTheDocument()
    expect(path).toBe('token-alpha')
  })
})
