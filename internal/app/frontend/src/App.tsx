import { Routes, Route, Navigate } from 'react-router-dom';
import { useState, useEffect } from 'react';
import NavBar from './components/NavBar';
import StatusBar from './components/StatusBar';
import Search from './pages/Search';
import Ask from './pages/Ask';
import type { AppStatus } from './types';

export default function App() {
  const [status, setStatus] = useState<AppStatus | null>(null);

  useEffect(() => {
    if (window.go?.app?.App?.GetStatus) {
      window.go.app.App.GetStatus().then(setStatus).catch(console.error);
    }
  }, []);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      <NavBar />
      <main style={{ flex: 1, overflow: 'auto', padding: '16px' }}>
        <Routes>
          <Route path="/search" element={<Search />} />
          <Route path="/ask" element={<Ask />} />
          <Route path="*" element={<Navigate to="/search" replace />} />
        </Routes>
      </main>
      <StatusBar status={status} />
    </div>
  );
}
