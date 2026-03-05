import type { AgentInfo } from '../types';

const containerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  marginBottom: '12px',
};

const selectStyle: React.CSSProperties = {
  padding: '8px 12px',
  fontSize: '14px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
};

const descStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
};

const clearBtnStyle: React.CSSProperties = {
  marginLeft: 'auto',
  padding: '4px 12px',
  fontSize: '12px',
  background: 'transparent',
  border: '1px solid #45475a',
  borderRadius: '4px',
  color: '#a6adc8',
  cursor: 'pointer',
};

interface Props {
  agents: AgentInfo[];
  selected: string;
  onChange: (id: string) => void;
  onClear: () => void;
}

export default function AgentSelector({ agents, selected, onChange, onClear }: Props) {
  const current = agents.find((a) => a.id === selected);

  return (
    <div style={containerStyle}>
      <label style={{ fontSize: '13px', color: '#a6adc8' }}>Agent:</label>
      <select
        value={selected}
        onChange={(e) => onChange(e.target.value)}
        style={selectStyle}
      >
        {agents.map((a) => (
          <option key={a.id} value={a.id}>{a.name}</option>
        ))}
      </select>
      {current && <span style={descStyle}>{current.description}</span>}
      <button onClick={onClear} style={clearBtnStyle}>Clear</button>
    </div>
  );
}
