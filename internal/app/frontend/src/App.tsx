import { Routes, Route, Navigate } from 'react-router-dom';
import { useState, useEffect, useCallback } from 'react';
import NavBar from './components/NavBar';
import StatusBar from './components/StatusBar';
import Search from './pages/Search';
import Ask from './pages/Ask';
import Sync from './pages/Sync';
import Faces from './pages/Faces';
import Settings from './pages/Settings';
import type { AppStatus } from './types';

export default function App() {
  const [status, setStatus] = useState<AppStatus | null>(null);

  const refreshStatus = useCallback(() => {
    if (window.go?.app?.App?.GetStatus) {
      window.go.app.App.GetStatus().then(setStatus).catch(console.error);
    }
  }, []);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      <NavBar />
      <main style={{ flex: 1, overflow: 'auto', padding: '16px' }}>
        <Routes>
          <Route path="/search" element={<Search status={status} />} />
          <Route path="/ask" element={<Ask status={status} />} />
          <Route path="/sync" element={<Sync status={status} onSyncComplete={refreshStatus} />} />
          <Route path="/faces" element={<Faces status={status} />} />
          <Route path="/settings" element={<Settings onConfigSaved={refreshStatus} />} />
          <Route path="*" element={<Navigate to="/search" replace />} />
        </Routes>
      </main>
      <StatusBar status={status} />
    </div>
  );
}
