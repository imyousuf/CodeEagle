import { useState, useRef, useEffect } from 'react';

const containerStyle: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  marginBottom: '12px',
};

const inputStyle: React.CSSProperties = {
  flex: 1,
  padding: '10px 14px',
  fontSize: '15px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
};

const buttonStyle: React.CSSProperties = {
  padding: '10px 20px',
  fontSize: '14px',
  fontWeight: 600,
  background: '#89b4fa',
  color: '#1e1e2e',
  border: 'none',
  borderRadius: '6px',
  cursor: 'pointer',
};

interface Props {
  onSearch: (query: string) => void;
  loading: boolean;
}

export default function SearchBar({ onSearch, loading }: Props) {
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        inputRef.current?.focus();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);

  const handleSubmit = () => {
    if (query.trim() && !loading) {
      onSearch(query.trim());
    }
  };

  return (
    <div style={containerStyle}>
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') handleSubmit(); }}
        placeholder="Semantic search... (Ctrl+K)"
        style={inputStyle}
        autoFocus
      />
      <button onClick={handleSubmit} disabled={loading} style={{
        ...buttonStyle,
        opacity: loading ? 0.6 : 1,
      }}>
        {loading ? 'Searching...' : 'Search'}
      </button>
    </div>
  );
}
