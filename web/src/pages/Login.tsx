import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, setToken } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import LangSwitch from '@/components/LangSwitch'

export default function LoginPage() {
  const navigate = useNavigate()
  const { d } = useI18n()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [captchaCode, setCaptchaCode] = useState('')
  const [captchaId, setCaptchaId] = useState('')
  const [captchaImg, setCaptchaImg] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // 验证码一次性使用：每次失败（或点击图片）都要换一张
  const refreshCaptcha = useCallback(async () => {
    setCaptchaCode('')
    try {
      const res = await api.getCaptcha()
      setCaptchaId(res.captcha_id)
      setCaptchaImg(res.image)
    } catch {
      setCaptchaId('')
      setCaptchaImg('')
      setError(d.login.captchaLoadFailed)
    }
  }, [d])

  useEffect(() => {
    refreshCaptcha()
  }, [refreshCaptcha])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await api.login(username, password, captchaId, captchaCode)
      setToken(res.token)
      navigate('/providers')
    } catch (err) {
      setError(err instanceof Error ? err.message : d.login.signInFailed)
      refreshCaptcha()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center">
      <div className="absolute right-4 top-4">
        <LangSwitch />
      </div>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">{d.login.title}</CardTitle>
          <CardDescription>{d.login.description}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">{d.login.username}</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">{d.login.password}</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="captcha">{d.login.captcha}</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="captcha"
                  value={captchaCode}
                  onChange={(e) => setCaptchaCode(e.target.value)}
                  autoComplete="off"
                  maxLength={4}
                  placeholder={d.login.captchaPlaceholder}
                  required
                />
                {captchaImg ? (
                  <img
                    src={captchaImg}
                    alt={d.login.captchaAlt}
                    title={d.login.captchaHint}
                    onClick={refreshCaptcha}
                    className="h-9 w-[108px] shrink-0 cursor-pointer rounded-md border"
                  />
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={refreshCaptcha}
                    className="h-9 w-[108px] shrink-0"
                  >
                    {d.login.loadCaptcha}
                  </Button>
                )}
              </div>
            </div>
            {error && <div className="text-sm text-destructive">{error}</div>}
            <Button type="submit" className="w-full" disabled={loading || !captchaId}>
              {loading ? d.login.signingIn : d.login.signIn}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
