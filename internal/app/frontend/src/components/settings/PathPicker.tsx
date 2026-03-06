import StatusDot from './StatusDot';
import type { ServiceStatus } from '../../types';

interface Props {
  value: string;
  onChange: (value: string) => void;
  onBrowse: () => void;
  placeholder?: string;
  status?: ServiceStatus | null;
}

const rowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
};

const inputStyle: React.CSSProperties = {
  flex: 1,
  padding: '6px 10px',
  fontSize: '13px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
  fontFamily: 'monospace',
};

const btnStyle: React.CSSProperties = {
  padding: '6px 12px',
  fontSize: '12px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#cdd6f4',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
};

export default function PathPicker({ value, onChange, onBrowse, placeholder, status }: Props) {
  const dotStatus = status
    ? status.available ? 'ok' : 'error'
    : value ? 'unknown' : 'unknown';

  return (
    <div style={rowStyle}>
      <input
        type="text"
        style={inputStyle}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder || 'Select a path...'}
      />
      <button type="button" style={btnStyle} onClick={onBrowse}>
        Browse...
      </button>
      {value && <StatusDot status={dotStatus} label={status?.message} />}
    </div>
  );
}
