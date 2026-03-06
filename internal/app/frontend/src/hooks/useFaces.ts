import { useState, useEffect, useCallback } from 'react';
import type { PersonInfo, FaceStats, CheckpointData } from '../types';

interface UseFacesReturn {
  persons: PersonInfo[];
  stats: FaceStats | null;
  checkpoint: CheckpointData | null;
  paused: boolean;
  loading: boolean;
  error: string | null;
  refresh: () => void;
  createPerson: (name: string, relationships: string[]) => Promise<void>;
  updatePerson: (id: string, name: string, relationships: string[]) => Promise<void>;
  deletePerson: (id: string) => Promise<void>;
  resumeSync: () => void;
}

export function useFaces(): UseFacesReturn {
  const [persons, setPersons] = useState<PersonInfo[]>([]);
  const [stats, setStats] = useState<FaceStats | null>(null);
  const [checkpoint, setCheckpoint] = useState<CheckpointData | null>(null);
  const [paused, setPaused] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    setLoading(true);
    Promise.all([
      window.go?.app?.App?.GetPersons?.() ?? Promise.resolve([]),
      window.go?.app?.App?.GetFaceStats?.() ?? Promise.resolve(null),
    ])
      .then(([p, s]) => {
        setPersons(p || []);
        setStats(s);
        setError(null);
      })
      .catch((err: Error) => setError(err.message || String(err)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Listen for checkpoint events.
  useEffect(() => {
    const cleanups: (() => void)[] = [];

    if (window.runtime?.EventsOn) {
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

      cleanups.push(
        window.runtime.EventsOn('sync:complete', () => {
          setPaused(false);
          setCheckpoint(null);
          refresh();
        })
      );
    }

    return () => cleanups.forEach(fn => fn());
  }, [refresh]);

  const createPerson = useCallback(async (name: string, relationships: string[]) => {
    await window.go.app.App.CreatePerson(name, relationships);
    refresh();
  }, [refresh]);

  const updatePerson = useCallback(async (id: string, name: string, relationships: string[]) => {
    await window.go.app.App.UpdatePerson(id, name, relationships);
    refresh();
  }, [refresh]);

  const deletePerson = useCallback(async (id: string) => {
    await window.go.app.App.DeletePerson(id);
    refresh();
  }, [refresh]);

  const resumeSync = useCallback(() => {
    window.go?.app?.App?.ResumeSync?.().catch(() => {});
  }, []);

  return {
    persons,
    stats,
    checkpoint,
    paused,
    loading,
    error,
    refresh,
    createPerson,
    updatePerson,
    deletePerson,
    resumeSync,
  };
}
