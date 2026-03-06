import SearchBar from '../components/SearchBar';
import SearchFiltersPanel from '../components/SearchFilters';
import CodeResultCard from '../components/CodeResultCard';
import DocResultCard from '../components/DocResultCard';
import { useSearch } from '../hooks/useSearch';
import type { AppStatus } from '../types';

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

const bannerStyle: React.CSSProperties = {
  padding: '16px 20px',
  background: '#1e1e2e',
  border: '1px solid #45475a',
  borderRadius: '8px',
  marginBottom: '16px',
};

const bannerTitle: React.CSSProperties = {
  fontSize: '14px',
  fontWeight: 600,
  color: '#f9e2af',
  marginBottom: '8px',
};

const bannerText: React.CSSProperties = {
  fontSize: '13px',
  color: '#a6adc8',
  lineHeight: '1.5',
};

const codeInline: React.CSSProperties = {
  background: '#313244',
  padding: '2px 6px',
  borderRadius: '4px',
  fontFamily: 'monospace',
  fontSize: '12px',
  color: '#89b4fa',
};

const summaryStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
  marginBottom: '8px',
};

interface Props {
  status: AppStatus | null;
}

export default function Search({ status }: Props) {
  const { results, loading, error, filters, setFilters, doSearch } = useSearch();

  const needsSetup = status && !status.vector_ready;

  return (
    <div style={{ maxWidth: '900px', margin: '0 auto' }}>
      {needsSetup && (
        <div style={bannerStyle}>
          <div style={bannerTitle}>Search is not available yet</div>
          <div style={bannerText}>
            {!status.graph_ready ? (
              <>
                The knowledge graph has not been built.
                Run <span style={codeInline}>codeeagle sync</span> to index your codebase,
                then <span style={codeInline}>codeeagle vectorindex</span> to build the search index.
              </>
            ) : (
              <>
                The knowledge graph is ready ({status.node_count} nodes), but the vector search
                index has not been built. Run <span style={codeInline}>codeeagle vectorindex</span> to
                enable semantic search.
              </>
            )}
          </div>
        </div>
      )}

      <SearchBar onSearch={doSearch} loading={loading} />
      <SearchFiltersPanel filters={filters} onChange={setFilters} />

      {error && <div style={errorStyle}>{error}</div>}

      {!results && !loading && !error && !needsSetup && (
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
