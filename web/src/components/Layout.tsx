import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import {
  Boxes,
  BrainCircuit,
  Globe,
  KeyRound,
  LogOut,
  Route as RouteIcon,
  ScrollText,
  Settings,
} from 'lucide-react'
import { setToken } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import LangSwitch from '@/components/LangSwitch'

export default function Layout() {
  const navigate = useNavigate()
  const { d } = useI18n()

  const navItems = [
    { to: '/providers', label: d.nav.providers, icon: Boxes },
    { to: '/models', label: d.nav.models, icon: BrainCircuit },
    { to: '/groups', label: d.nav.groups, icon: RouteIcon },
    { to: '/keys', label: d.nav.keys, icon: KeyRound },
    { to: '/web-tools', label: d.nav.webTools, icon: Globe },
    { to: '/logs', label: d.nav.logs, icon: ScrollText },
    { to: '/settings', label: d.nav.settings, icon: Settings },
  ]

  const logout = () => {
    setToken(null)
    navigate('/login')
  }

  return (
    <div className="flex min-h-screen">
      <aside className="w-56 shrink-0 border-r bg-card flex flex-col">
        <div className="px-5 py-5 border-b">
          <div className="text-lg font-bold tracking-tight">VibeRouter</div>
          <div className="text-xs text-muted-foreground mt-0.5">{d.app.subtitle}</div>
        </div>
        <nav className="flex-1 p-3 space-y-1">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              data-testid={`nav-${item.to.slice(1)}`}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                  isActive
                    ? 'bg-accent text-accent-foreground font-medium'
                    : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground',
                )
              }
            >
              <item.icon className="h-4 w-4" />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="p-3 border-t space-y-1">
          <LangSwitch />
          <Button variant="ghost" className="w-full justify-start gap-3" data-testid="nav-logout" onClick={logout}>
            <LogOut className="h-4 w-4" />
            {d.nav.logout}
          </Button>
        </div>
      </aside>
      <main className="flex-1 min-w-0 p-6">
        <Outlet />
      </main>
    </div>
  )
}
