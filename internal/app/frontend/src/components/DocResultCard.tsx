import type { DocResult } from '../types';

const cardStyle: React.CSSProperties = {
  padding: '12px 16px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  marginBottom: '8px',
};

const headerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  marginBottom: '4px',
};

const nameStyle: React.CSSProperties = {
  fontWeight: 600,
  fontSize: '14px',
  color: '#cdd6f4',
};

const badgeStyle: React.CSSProperties = {
  padding: '2px 6px',
  fontSize: '11px',
  fontWeight: 600,
  borderRadius: '3px',
  background: '#585b70',
  color: '#cdd6f4',
};

const metaStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
  marginBottom: '4px',
};

const snippetStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#bac2de',
  whiteSpace: 'pre-wrap',
  overflow: 'hidden',
  maxHeight: '48px',
};

const relevanceStyle = (relevance: number): React.CSSProperties => ({
  fontSize: '12px',
  fontWeight: 600,
  color: relevance >= 50 ? '#a6e3a1' : relevance >= 30 ? '#f9e2af' : '#f38ba8',
});

interface Props {
  result: DocResult;
}

export default function DocResultCard({ result }: Props) {
  return (
    <div style={cardStyle}>
      <div style={headerStyle}>
        <span style={badgeStyle}>{result.type}</span>
        <span style={nameStyle}>{result.name}</span>
        <span style={relevanceStyle(result.relevance)}>{result.relevance}%</span>
      </div>
      {result.file_path && <div style={metaStyle}>{result.file_path}</div>}
      {result.snippet && <div style={snippetStyle}>{result.snippet}</div>}
    </div>
  );
}
