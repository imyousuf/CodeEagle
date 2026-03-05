import { useState, useCallback } from 'react';
import type { SearchFilters, SearchResults } from '../types';
import { defaultFilters } from '../types';

export function useSearch() {
  const [results, setResults] = useState<SearchResults | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filters, setFilters] = useState<SearchFilters>(defaultFilters);

  const doSearch = useCallback(async (query: string) => {
    if (!window.go?.app?.App?.Search) {
      setError('Search backend not available');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const res = await window.go.app.App.Search(query, filters);
      setResults(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setResults(null);
    } finally {
      setLoading(false);
    }
  }, [filters]);

  return { results, loading, error, filters, setFilters, doSearch };
}
