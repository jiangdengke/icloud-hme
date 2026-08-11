import { useRef, useState, type FormEvent } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'

interface AccountFormDialogProps {
  open: boolean
  onClose: () => void
  onSaved: () => void
  editing?: {
    id: string
    name: string
    icloudEmail: string
    host: string
  } | null
}

/** 添加账号 / 编辑基本信息对话框 */
export default function AccountFormDialog({
  open,
  onClose,
  onSaved,
  editing,
}: AccountFormDialogProps) {
  const [name, setName] = useState(editing?.name ?? '')
  const [icloudEmail, setIcloudEmail] = useState(editing?.icloudEmail ?? '')
  const [host, setHost] = useState(editing?.host ?? 'icloud.com.cn')
  const [cookies, setCookies] = useState('')
  const [proxy, setProxy] = useState('')
  const [error, setError] = useState('')
  const [emailError, setEmailError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const emailRef = useRef<HTMLInputElement>(null)

  function reset() {
    setName('')
    setIcloudEmail('')
    setHost('icloud.com.cn')
    setCookies('')
    setProxy('')
    setError('')
    setEmailError('')
    setSubmitting(false)
  }

  function generatedName(): string {
    const bytes = new Uint8Array(2)
    crypto.getRandomValues(bytes)
    return `账号-${Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('').toUpperCase()}`
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submitting) return
    if (editing && !name.trim()) {
      setError('请输入账号名称')
      return
    }
    const normalizedEmail = icloudEmail.trim()
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail)) {
      setEmailError('请输入完整邮箱地址')
      emailRef.current?.focus()
      return
    }
    setSubmitting(true)
    setError('')
    try {
      if (editing) {
        await request(`/api/accounts/${editing.id}`, {
          method: 'PATCH',
          body: JSON.stringify({ name: name.trim(), icloud_email: normalizedEmail, host }),
        })
      } else {
        await request('/api/accounts', {
          method: 'POST',
          body: JSON.stringify({
            name: generatedName(),
            icloud_email: normalizedEmail,
            host,
            proxy,
            cookies,
          }),
        })
      }
      reset()
      onSaved()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      title={editing ? '编辑账号' : '接入已有账号'}
      open={open}
      onClose={() => {
        reset()
        onClose()
      }}
    >
      <form onSubmit={(event) => void handleSubmit(event)} noValidate>
        {!editing && (
          <div className="alert-info">
            接入一个已有账号后，即可在“生成邮箱”中一键创建随机地址。
          </div>
        )}
        {error && (
          <div className="alert-error" role="alert">
            {error}
          </div>
        )}
        {editing && (
          <div className="form-field">
            <label htmlFor="acc-name">名称</label>
            <input
              id="acc-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={64}
            />
          </div>
        )}
        <div className="form-field">
          <label htmlFor="acc-email">已有账号邮箱</label>
          <input
            ref={emailRef}
            id="acc-email"
            type="email"
            value={icloudEmail}
            onChange={(e) => {
              setIcloudEmail(e.target.value)
              setEmailError('')
            }}
            disabled={Boolean(editing)}
            aria-invalid={emailError ? 'true' : undefined}
            aria-describedby={emailError ? 'acc-email-error' : undefined}
            placeholder="name@example.com"
          />
          {emailError && (
            <p id="acc-email-error" className="field-error" role="alert">
              {emailError}
            </p>
          )}
        </div>
        <div className="form-field">
          <label htmlFor="acc-host">区域</label>
          <select
            id="acc-host"
            value={host}
            onChange={(e) => setHost(e.target.value)}
          >
            <option value="icloud.com">全球区 (icloud.com)</option>
            <option value="icloud.com.cn">中国区 (icloud.com.cn)</option>
          </select>
        </div>
        <div className="form-actions">
          <button type="button" onClick={onClose}>取消</button>
          <button type="submit" className="primary" disabled={submitting}>
            {submitting ? '保存中…' : '保存'}
          </button>
        </div>
      </form>
    </Dialog>
  )
}
