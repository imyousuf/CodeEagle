type Status = 'ok' | 'error' | 'checking' | 'unknown';

interface Props {
  status: Status;
  label?: string;
}

const colors: Record<Status, string> = {
  ok: '#a6e3a1',
  error: '#f38ba8',
  checking: '#f9e2af',
  unknown: '#585b70',
};

const dotStyle = (status: Status): React.CSSProperties => ({
  display: 'inline-block',
  width: '8px',
  height: '8px',
  borderRadius: '50%',
  background: colors[status],
  marginRight: '6px',
  verticalAlign: 'middle',
  animation: status === 'checking' ? 'pulse 1s infinite' : undefined,
});

const labelStyle: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
  verticalAlign: 'middle',
};

export default function StatusDot({ status, label }: Props) {
  return (
    <span>
      <span style={dotStyle(status)} />
      {label && <span style={labelStyle}>{label}</span>}
    </span>
  );
}
