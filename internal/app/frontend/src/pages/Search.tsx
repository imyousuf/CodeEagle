import SearchBar from '../components/SearchBar';
import SearchFiltersPanel from '../components/SearchFilters';
import CodeResultCard from '../components/CodeResultCard';
import DocResultCard from '../components/DocResultCard';
import { useSearch } from '../hooks/useSearch';

const sectionTitle: React.CSSProperties = {
  fontSize: '13px',
  fontWeight: 600,
  color: '#a6adc8',
  textTransform: 'uppercase',
  letterSpacing: '0.5px',
  marginBottom: '8px',
  marginTop: '16px',
};

const emptyStyle: React.CSSProperties = {
  textAlign: 'center',
  padding: '48px 0',
  color: '#a6adc8',
  fontSize: '14px',
};

const errorStyle: React.CSSProperties = {
  padding: '12px 16px',
  background: '#302030',
  border: '1px solid #f38ba8',
  borderRadius: '6px',
  color: '#f38ba8',
  fontSize: '13px',
  marginBottom: '12px',
};

const summaryStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
  marginBottom: '8px',
};

export default function Search() {
  const { results, loading, error, filters, setFilters, doSearch } = useSearch();

  return (
    <div style={{ maxWidth: '900px', margin: '0 auto' }}>
      <SearchBar onSearch={doSearch} loading={loading} />
      <SearchFiltersPanel filters={filters} onChange={setFilters} />

      {error && <div style={errorStyle}>{error}</div>}

      {!results && !loading && !error && (
        <div style={emptyStyle}>
          <p>Search the knowledge graph using natural language.</p>
          <p style={{ marginTop: '8px', fontSize: '12px' }}>
            If no results appear, run <code>codeeagle sync</code> to build the index.
          </p>
        </div>
      )}

      {results && results.total === 0 && (
        <div style={emptyStyle}>No results found.</div>
      )}

      {results && (results.code?.length ?? 0) > 0 && (
        <>
          <div style={sectionTitle}>Code ({results.code!.length})</div>
          {results.code!.map((r, i) => (
            <CodeResultCard key={`code-${i}`} result={r} />
          ))}
        </>
      )}

      {results && (results.docs?.length ?? 0) > 0 && (
        <>
          <div style={sectionTitle}>Documentation ({results.docs!.length})</div>
          {results.docs!.map((r, i) => (
            <DocResultCard key={`doc-${i}`} result={r} />
          ))}
        </>
      )}

      {results && results.provider && (
        <div style={summaryStyle}>
          {results.total} results (embedding: {results.provider})
        </div>
      )}
    </div>
  );
}
