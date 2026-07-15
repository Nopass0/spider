/**
 * Корневой компонент: роутинг + guard по admin-ключу.
 */
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { useAuthStore } from '@/stores/auth';
import { Layout } from './Layout';
import { LoginPage } from '@/pages/Login';
import { DevicesPage } from '@/pages/Devices';
import { DeviceDetailPage } from '@/pages/DeviceDetail';
import { EnrollmentsPage } from '@/pages/Enrollments';
import { SettingsPage } from '@/pages/Settings';

export function App() {
  const key = useAuthStore((s) => s.key);

  if (!key) {
    return <LoginPage />;
  }

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Navigate to="/devices" replace />} />
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/devices/:id" element={<DeviceDetailPage />} />
          <Route path="/enrollments" element={<EnrollmentsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/devices" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
