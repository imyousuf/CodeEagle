import type { AppStatus } from '../types';

const barStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '16px',
  padding: '4px 16px',
  height: '28px',
  background: '#181825',
  borderTop: '1px solid #45475a',
  fontSize: '12px',
  color: '#a6adc8',
  flexShrink: 0,
};

const dotStyle = (ready: boolean): React.CSSProperties => ({
  display: 'inline-block',
  width: '6px',
  height: '6px',
  borderRadius: '50%',
  background: ready ? '#a6e3a1' : '#f38ba8',
  marginRight: '4px',
});

interface Props {
  status: AppStatus | null;
}

export default function StatusBar({ status }: Props) {
  if (!status) {
    return (
      <div style={barStyle}>
        <span>Loading status...</span>
      </div>
    );
  }

  return (
    <div style={barStyle}>
      {status.project_name && <span>{status.project_name}</span>}
      <span>
        <span style={dotStyle(status.graph_ready)} />
        {status.node_count} nodes / {status.edge_count} edges
      </span>
      <span>
        <span style={dotStyle(status.vector_ready)} />
        {status.vector_ready ? `${status.vector_count} vectors` : 'No vector index'}
      </span>
      {status.embed_provider && <span>{status.embed_provider}</span>}
      <span>
        <span style={dotStyle(status.llm_ready)} />
        {status.llm_ready ? status.llm_provider : 'No LLM'}
      </span>
      {status.branch && <span>{status.branch}</span>}
    </div>
  );
}
