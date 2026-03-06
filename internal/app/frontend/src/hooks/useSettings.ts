import { useState, useEffect, useCallback } from 'react';
import type {
  ConfigDTO,
  DetectionResult,
  ServiceStatus,
  ConfigDiff,
} from '../types';

// Deep clone helper for config objects.
function cloneConfig(cfg: ConfigDTO): ConfigDTO {
  return JSON.parse(JSON.stringify(cfg));
}

// Deep equality check.
function configsEqual(a: ConfigDTO, b: ConfigDTO): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function useSettings(onConfigSaved?: () => void) {
  const [config, setConfig] = useState<ConfigDTO | null>(null);
  const [draft, setDraft] = useState<ConfigDTO | null>(null);
  const [allLanguages, setAllLanguages] = useState<string[]>([]);
  const [configPath, setConfigPath] = useState('');
  const [detection, setDetection] = useState<DetectionResult | null>(null);
  const [saving, setSaving] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, ServiceStatus>>({});

  const isDirty = config !== null && draft !== null && !configsEqual(config, draft);

  // Load config on mount.
  useEffect(() => {
    const api = window.go?.app?.App;
    if (!api) return;

    Promise.all([
      api.GetConfig(),
      api.GetAllLanguages(),
      api.GetConfigPath(),
    ]).then(([cfg, langs, path]) => {
      setConfig(cfg);
      setDraft(cloneConfig(cfg));
      setAllLanguages(langs);
      setConfigPath(path);
    }).catch(console.error);
  }, []);

  // Update a nested field in the draft.
  const updateDraft = useCallback(<K extends keyof ConfigDTO>(
    section: K,
    field: string,
    value: unknown,
  ) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (next[section] as any)[field] = value;
      return next;
    });
  }, []);

  // Update top-level draft fields (languages, repositories, watch).
  const updateDraftField = useCallback(<K extends keyof ConfigDTO>(
    key: K,
    value: ConfigDTO[K],
  ) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next[key] = value;
      return next;
    });
  }, []);

  // Update nested docs.faces fields.
  const updateFaces = useCallback((field: string, value: unknown) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (next.docs.faces as any)[field] = value;
      return next;
    });
  }, []);

  // Auto-detect all.
  const detectAll = useCallback(async () => {
    const api = window.go?.app?.App;
    if (!api) return;
    setDetecting(true);
    try {
      const result = await api.DetectAll();
      setDetection(result);
      // Apply detected values to draft.
      setDraft(prev => {
        if (!prev) return prev;
        const next = cloneConfig(prev);
        if (result.llm_provider) {
          next.agents.llm_provider = result.llm_provider;
        }
        if (result.languages && result.languages.length > 0) {
          next.languages = result.languages;
        }
        return next;
      });
    } catch (err) {
      console.error('Detection failed:', err);
    } finally {
      setDetecting(false);
    }
  }, []);

  // Detect languages for a specific path.
  const detectLanguagesForPath = useCallback(async (path: string) => {
    const api = window.go?.app?.App;
    if (!api || !path) return;
    try {
      const langs = await api.DetectLanguages(path);
      if (langs && langs.length > 0) {
        setDraft(prev => {
          if (!prev) return prev;
          const next = cloneConfig(prev);
          const existing = new Set(next.languages);
          for (const l of langs) existing.add(l);
          next.languages = Array.from(existing);
          return next;
        });
      }
    } catch (err) {
      console.error('Language detection failed:', err);
    }
  }, []);

  // Browse directory.
  const browseDirectory = useCallback(async (title: string): Promise<string> => {
    const api = window.go?.app?.App;
    if (!api) return '';
    try {
      return await api.BrowseDirectory(title);
    } catch {
      return '';
    }
  }, []);

  // Browse file.
  const browseFile = useCallback(async (title: string, filter: string): Promise<string> => {
    const api = window.go?.app?.App;
    if (!api) return '';
    try {
      return await api.BrowseFile(title, filter);
    } catch {
      return '';
    }
  }, []);

  // Test LLM connection.
  const testLLM = useCallback(async () => {
    const api = window.go?.app?.App;
    if (!api || !draft) return;
    setTestResults(prev => ({ ...prev, llm: { available: false, message: 'Testing...' } }));
    try {
      const result = await api.TestLLMConnection(
        draft.agents.llm_provider,
        draft.agents.model,
        draft.agents.project,
        draft.agents.location,
        draft.agents.credentials_file,
        draft.agents.base_url,
      );
      setTestResults(prev => ({ ...prev, llm: result }));
    } catch (err) {
      setTestResults(prev => ({ ...prev, llm: { available: false, message: String(err) } }));
    }
  }, [draft]);

  // Test Ollama.
  const testOllama = useCallback(async (baseURL?: string) => {
    const api = window.go?.app?.App;
    if (!api) return;
    setTestResults(prev => ({ ...prev, ollama: { available: false, message: 'Testing...' } }));
    try {
      const result = await api.TestOllamaConnection(baseURL || '');
      setTestResults(prev => ({ ...prev, ollama: result }));
    } catch (err) {
      setTestResults(prev => ({ ...prev, ollama: { available: false, message: String(err) } }));
    }
  }, []);

  // Validate path.
  const validatePath = useCallback(async (path: string, expectDir: boolean): Promise<ServiceStatus> => {
    const api = window.go?.app?.App;
    if (!api) return { available: false, message: 'Backend not available' };
    try {
      return await api.ValidatePath(path, expectDir);
    } catch (err) {
      return { available: false, message: String(err) };
    }
  }, []);

  // Preview changes.
  const previewChanges = useCallback(async (): Promise<ConfigDiff[]> => {
    const api = window.go?.app?.App;
    if (!api || !draft) return [];
    try {
      return await api.PreviewConfigChanges(draft);
    } catch {
      return [];
    }
  }, [draft]);

  // Save config.
  const saveConfig = useCallback(async () => {
    const api = window.go?.app?.App;
    if (!api || !draft) return;
    setSaving(true);
    setSaveMessage(null);
    try {
      const result = await api.SaveConfig(draft);
      if (result.success) {
        setConfig(cloneConfig(draft));
        setSaveMessage(result.message);
        onConfigSaved?.();
      } else {
        setSaveMessage(result.message);
      }
    } catch (err) {
      setSaveMessage(String(err));
    } finally {
      setSaving(false);
    }
  }, [draft]);

  // Reset draft to saved config.
  const resetDraft = useCallback(() => {
    if (config) {
      setDraft(cloneConfig(config));
      setSaveMessage(null);
    }
  }, [config]);

  // Repository management.
  const addRepository = useCallback(() => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next.repositories = [...next.repositories, { path: '', type: 'single' }];
      return next;
    });
  }, []);

  const removeRepository = useCallback((index: number) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next.repositories = next.repositories.filter((_, i) => i !== index);
      return next;
    });
  }, []);

  const updateRepository = useCallback((index: number, field: 'path' | 'type', value: string) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next.repositories = next.repositories.map((r, i) =>
        i === index ? { ...r, [field]: value } : r
      );
      return next;
    });
  }, []);

  // Watch exclusion management.
  const addExcludePattern = useCallback((pattern: string) => {
    setDraft(prev => {
      if (!prev || !pattern.trim()) return prev;
      const next = cloneConfig(prev);
      next.watch.exclude = [...next.watch.exclude, pattern.trim()];
      return next;
    });
  }, []);

  const removeExcludePattern = useCallback((index: number) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next.watch.exclude = next.watch.exclude.filter((_, i) => i !== index);
      return next;
    });
  }, []);

  // Docs exclude extensions management.
  const addExcludeExtension = useCallback((ext: string) => {
    setDraft(prev => {
      if (!prev || !ext.trim()) return prev;
      const next = cloneConfig(prev);
      next.docs.exclude_extensions = [...next.docs.exclude_extensions, ext.trim()];
      return next;
    });
  }, []);

  const removeExcludeExtension = useCallback((index: number) => {
    setDraft(prev => {
      if (!prev) return prev;
      const next = cloneConfig(prev);
      next.docs.exclude_extensions = next.docs.exclude_extensions.filter((_, i) => i !== index);
      return next;
    });
  }, []);

  return {
    config,
    draft,
    allLanguages,
    configPath,
    detection,
    isDirty,
    saving,
    detecting,
    saveMessage,
    testResults,
    updateDraft,
    updateDraftField,
    updateFaces,
    detectAll,
    detectLanguagesForPath,
    browseDirectory,
    browseFile,
    testLLM,
    testOllama,
    validatePath,
    previewChanges,
    saveConfig,
    resetDraft,
    addRepository,
    removeRepository,
    updateRepository,
    addExcludePattern,
    removeExcludePattern,
    addExcludeExtension,
    removeExcludeExtension,
  };
}
