import type { SearchFilters as Filters } from '../types';

const rowStyle: React.CSSProperties = {
  display: 'flex',
  gap: '12px',
  alignItems: 'center',
  marginBottom: '16px',
  flexWrap: 'wrap',
};

const selectStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '4px',
  color: '#cdd6f4',
  outline: 'none',
};

const labelStyle: React.CSSProperties = {
  fontSize: '13px',
  color: '#a6adc8',
  display: 'flex',
  alignItems: 'center',
  gap: '4px',
};

const nodeTypes = [
  '', 'Function', 'Method', 'Struct', 'Class', 'Interface',
  'Enum', 'Type', 'APIEndpoint', 'TestFunction', 'Document',
];

const languages = [
  '', 'go', 'python', 'typescript', 'javascript', 'java',
  'rust', 'csharp', 'ruby', 'html', 'markdown',
];

interface Props {
  filters: Filters;
  onChange: (filters: Filters) => void;
}

export default function SearchFilters({ filters, onChange }: Props) {
  return (
    <div style={rowStyle}>
      <label style={labelStyle}>
        Type:
        <select
          value={filters.node_type}
          onChange={(e) => onChange({ ...filters, node_type: e.target.value })}
          style={selectStyle}
        >
          {nodeTypes.map((t) => (
            <option key={t} value={t}>{t || 'All'}</option>
          ))}
        </select>
      </label>
      <label style={labelStyle}>
        Language:
        <select
          value={filters.language}
          onChange={(e) => onChange({ ...filters, language: e.target.value })}
          style={selectStyle}
        >
          {languages.map((l) => (
            <option key={l} value={l}>{l || 'All'}</option>
          ))}
        </select>
      </label>
      <label style={labelStyle}>
        Package:
        <input
          type="text"
          value={filters.package}
          onChange={(e) => onChange({ ...filters, package: e.target.value })}
          placeholder="filter..."
          style={{ ...selectStyle, width: '120px' }}
        />
      </label>
      <label style={labelStyle}>
        <input
          type="checkbox"
          checked={filters.no_docs}
          onChange={(e) => onChange({ ...filters, no_docs: e.target.checked })}
        />
        No docs
      </label>
    </div>
  );
}
