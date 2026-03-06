import { useState, useEffect, useCallback, useRef } from 'react';
import type { CheckpointData } from '../types';

interface LogEntry {
  timestamp: string;
  message: string;
}

interface UseSyncReturn {
  syncing: boolean;
  logs: LogEntry[];
  error: string | null;
  completed: boolean;
  currentPhase: string;
  checkpoint: CheckpointData | null;
  paused: boolean;
  startSync: (full: boolean) => void;
  clearLogs: () => void;
  resumeSync: () => void;
}

export function useSync(onComplete?: () => void): UseSyncReturn {
  const [syncing, setSyncing] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [completed, setCompleted] = useState(false);
  const [currentPhase, setCurrentPhase] = useState('');
  const [checkpoint, setCheckpoint] = useState<CheckpointData | null>(null);
  const [paused, setPaused] = useState(false);
  const onCompleteRef = useRef(onComplete);
  onCompleteRef.current = onComplete;

  // Check initial sync state.
  useEffect(() => {
    window.go?.app?.App?.IsSyncing?.().then(setSyncing).catch(() => {});
  }, []);

  // Listen for Wails events.
  useEffect(() => {
    const cleanups: (() => void)[] = [];

    if (window.runtime?.EventsOn) {
      cleanups.push(
        window.runtime.EventsOn('sync:log', (...args: unknown[]) => {
          const msg = String(args[0] ?? '');
          const now = new Date().toLocaleTimeString();
          setLogs(prev => [...prev, { timestamp: now, message: msg }]);
        })
      );

      cleanups.push(
        window.runtime.EventsOn('sync:complete', () => {
          setSyncing(false);
          setCompleted(true);
          setCurrentPhase('');
          setPaused(false);
          setCheckpoint(null);
          onCompleteRef.current?.();
        })
      );

      cleanups.push(
        window.runtime.EventsOn('sync:error', (...args: unknown[]) => {
          const msg = String(args[0] ?? 'unknown error');
          setSyncing(false);
          setError(msg);
          setCurrentPhase('');
        })
      );

      cleanups.push(
        window.runtime.EventsOn('sync:phase', (...args: unknown[]) => {
          const phase = String(args[0] ?? '');
          setCurrentPhase(phase);
        })
      );

      cleanups.push(
        window.runtime.EventsOn('sync:checkpoint', (...args: unknown[]) => {
          const data = args[0] as CheckpointData | undefined;
          if (data) {
            setCheckpoint(data);
            setPaused(true);
          }
        })
      );

      cleanups.push(
        window.runtime.EventsOn('sync:resumed', () => {
          setCheckpoint(null);
          setPaused(false);
        })
      );

      // System notifications.
      cleanups.push(
        window.runtime.EventsOn('notification:show', (...args: unknown[]) => {
          const data = args[0] as { title?: string; body?: string } | undefined;
          if (data && 'Notification' in window) {
            if (Notification.permission === 'granted') {
              new Notification(data.title || 'CodeEagle', { body: data.body });
            } else if (Notification.permission !== 'denied') {
              Notification.requestPermission().then(p => {
                if (p === 'granted') {
                  new Notification(data.title || 'CodeEagle', { body: data.body });
                }
              });
            }
          }
        })
      );
    }

    return () => cleanups.forEach(fn => fn());
  }, []);

  const startSync = useCallback((full: boolean) => {
    setError(null);
    setCompleted(false);
    setLogs([]);
    setSyncing(true);
    setCurrentPhase('');
    setPaused(false);
    setCheckpoint(null);

    window.go.app.App.StartSync(full).catch((err: Error) => {
      setSyncing(false);
      setError(err.message || String(err));
    });
  }, []);

  const clearLogs = useCallback(() => {
    setLogs([]);
    setError(null);
    setCompleted(false);
    setCurrentPhase('');
  }, []);

  const resumeSync = useCallback(() => {
    window.go?.app?.App?.ResumeSync?.().catch(() => {});
  }, []);

  return {
    syncing, logs, error, completed, currentPhase,
    checkpoint, paused, startSync, clearLogs, resumeSync,
  };
}
