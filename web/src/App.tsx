import { Navigate, Route, Routes } from 'react-router-dom'
import { getToken } from '@/lib/api'
import Layout from '@/components/Layout'
import LoginPage from '@/pages/Login'
import ProvidersPage from '@/pages/Providers'
import ModelsPage from '@/pages/Models'
import GroupsPage from '@/pages/Groups'
import KeysPage from '@/pages/Keys'
import LogsPage from '@/pages/Logs'
import WebToolsPage from '@/pages/WebTools'
import SettingsPage from '@/pages/Settings'

function RequireAuth({ children }: { children: JSX.Element }) {
  if (!getToken()) return <Navigate to="/login" replace />
  return children
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="/providers" replace />} />
        <Route path="providers" element={<ProvidersPage />} />
        <Route path="models" element={<ModelsPage />} />
        <Route path="groups" element={<GroupsPage />} />
        <Route path="keys" element={<KeysPage />} />
        <Route path="web-tools" element={<WebToolsPage />} />
        <Route path="logs" element={<LogsPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
