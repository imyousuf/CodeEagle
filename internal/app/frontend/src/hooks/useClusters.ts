import { useState, useEffect, useCallback } from 'react';
import type { ClusterDetail, ClusterFace, MergeSuggestion, ClusteringProgress } from '../types';

interface UseClustersReturn {
  clusters: ClusterDetail[];
  noiseFaces: ClusterFace[];
  suggestions: MergeSuggestion[];
  loading: boolean;
  clusteringInProgress: boolean;
  clusteringProgress: ClusteringProgress | null;
  error: string | null;
  refresh: () => void;
  runClustering: (simThreshold: number) => Promise<void>;
  removeFace: (imagePath: string, faceIndex: number) => Promise<void>;
  mergeClusters: (targetID: number, sourceIDs: number[]) => Promise<void>;
  splitCluster: (clusterID: number, simThreshold: number) => Promise<void>;
  setLabel: (clusterID: number, label: string) => Promise<void>;
  assignToPerson: (clusterID: number, personID: string) => Promise<void>;
  loadSuggestions: () => Promise<void>;
}

export function useClusters(): UseClustersReturn {
  const [clusters, setClusters] = useState<ClusterDetail[]>([]);
  const [noiseFaces, setNoiseFaces] = useState<ClusterFace[]>([]);
  const [suggestions, setSuggestions] = useState<MergeSuggestion[]>([]);
  const [loading, setLoading] = useState(true);
  const [clusteringInProgress, setClusteringInProgress] = useState(false);
  const [clusteringProgress, setClusteringProgress] = useState<ClusteringProgress | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    setLoading(true);
    Promise.all([
      window.go?.app?.App?.GetClusters?.() ?? Promise.resolve([]),
      window.go?.app?.App?.GetNoiseFaces?.() ?? Promise.resolve([]),
      window.go?.app?.App?.IsClusteringRunning?.() ?? Promise.resolve(false),
    ])
      .then(([c, n, running]) => {
        setClusters(c || []);
        setNoiseFaces(n || []);
        setClusteringInProgress(running);
        setError(null);
      })
      .catch((err: Error) => setError(err.message || String(err)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Listen for clustering events.
  useEffect(() => {
    const cleanups: (() => void)[] = [];

    if (window.runtime?.EventsOn) {
      cleanups.push(
        window.runtime.EventsOn('faces:clustering-started', () => {
          setClusteringInProgress(true);
          setClusteringProgress(null);
          setError(null);
        })
      );

      cleanups.push(
        window.runtime.EventsOn('faces:clustering-progress', (...args: unknown[]) => {
          const data = args[0] as ClusteringProgress | undefined;
          if (data) {
            setClusteringProgress(data);
          }
        })
      );

      cleanups.push(
        window.runtime.EventsOn('faces:clustering-complete', () => {
          setClusteringInProgress(false);
          setClusteringProgress(null);
          refresh();
        })
      );

      cleanups.push(
        window.runtime.EventsOn('faces:clustering-error', (...args: unknown[]) => {
          setClusteringInProgress(false);
          setClusteringProgress(null);
          const msg = typeof args[0] === 'string' ? args[0] : 'Clustering failed';
          setError(msg);
        })
      );
    }

    return () => cleanups.forEach(fn => fn());
  }, [refresh]);

  const runClustering = useCallback(async (simThreshold: number) => {
    setError(null);
    await window.go.app.App.RunClustering(simThreshold);
  }, []);

  const removeFace = useCallback(async (imagePath: string, faceIndex: number) => {
    await window.go.app.App.RemoveFaceFromCluster(imagePath, faceIndex);
    refresh();
  }, [refresh]);

  const mergeClusters = useCallback(async (targetID: number, sourceIDs: number[]) => {
    await window.go.app.App.MergeClusters(targetID, sourceIDs);
    refresh();
  }, [refresh]);

  const splitCluster = useCallback(async (clusterID: number, simThreshold: number) => {
    await window.go.app.App.SplitCluster(clusterID, simThreshold);
    refresh();
  }, [refresh]);

  const setLabel = useCallback(async (clusterID: number, label: string) => {
    await window.go.app.App.SetClusterLabel(clusterID, label);
    refresh();
  }, [refresh]);

  const assignToPerson = useCallback(async (clusterID: number, personID: string) => {
    await window.go.app.App.AssignClusterToPerson(clusterID, personID);
    refresh();
  }, [refresh]);

  const loadSuggestions = useCallback(async () => {
    try {
      const s = await window.go.app.App.GetSuggestedMerges();
      setSuggestions(s || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  return {
    clusters,
    noiseFaces,
    suggestions,
    loading,
    clusteringInProgress,
    clusteringProgress,
    error,
    refresh,
    runClustering,
    removeFace,
    mergeClusters,
    splitCluster,
    setLabel,
    assignToPerson,
    loadSuggestions,
  };
}
