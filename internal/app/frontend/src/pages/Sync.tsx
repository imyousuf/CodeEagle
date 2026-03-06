import { useRef, useEffect } from 'react';
import { useSync } from '../hooks/useSync';
import type { AppStatus } from '../types';

interface SyncProps {
  status: AppStatus | null;
  onSyncComplete?: () => void;
}

const headerStyle: React.CSSProperties = {
  marginBottom: '16px',
};

const titleStyle: React.CSSProperties = {
  fontSize: '18px',
  fontWeight: 600,
  color: '#cdd6f4',
  margin: '0 0 4px 0',
};

const subtitleStyle: React.CSSProperties = {
  fontSize: '13px',
  color: '#a6adc8',
  margin: 0,
};

const buttonRow: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  marginBottom: '12px',
};

const btnBase: React.CSSProperties = {
  padding: '8px 16px',
  fontSize: '13px',
  fontWeight: 600,
  border: 'none',
  borderRadius: '6px',
  cursor: 'pointer',
  transition: 'opacity 0.15s',
};

const btnPrimary: React.CSSProperties = {
  ...btnBase,
  background: '#89b4fa',
  color: '#1e1e2e',
};

const btnSecondary: React.CSSProperties = {
  ...btnBase,
  background: '#45475a',
  color: '#cdd6f4',
};

const btnDisabled: React.CSSProperties = {
  opacity: 0.5,
  cursor: 'not-allowed',
};

const logContainer: React.CSSProperties = {
  flex: 1,
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: '6px',
  padding: '12px',
  overflow: 'auto',
  fontFamily: 'monospace',
  fontSize: '12px',
  lineHeight: '1.6',
  color: '#cdd6f4',
  minHeight: '300px',
  maxHeight: 'calc(100vh - 280px)',
};

const timestampStyle: React.CSSProperties = {
  color: '#585b70',
  marginRight: '8px',
  userSelect: 'none',
};

const emptyStyle: React.CSSProperties = {
  textAlign: 'center',
  padding: '48px 0',
  color: '#a6adc8',
  fontSize: '14px',
};

const bannerBase: React.CSSProperties = {
  padding: '10px 14px',
  borderRadius: '6px',
  fontSize: '13px',
  marginBottom: '12px',
};

const errorBanner: React.CSSProperties = {
  ...bannerBase,
  background: '#302030',
  border: '1px solid #f38ba8',
  color: '#f38ba8',
};

const successBanner: React.CSSProperties = {
  ...bannerBase,
  background: '#203020',
  border: '1px solid #a6e3a1',
  color: '#a6e3a1',
};

const checkpointBanner: React.CSSProperties = {
  ...bannerBase,
  background: '#302040',
  border: '1px solid #cba6f7',
  color: '#cba6f7',
};

const checkpointRow: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
};

const phaseLabel: React.CSSProperties = {
  fontSize: '12px',
  color: '#89b4fa',
  fontWeight: 600,
  marginBottom: '4px',
};

export default function Sync({ status, onSyncComplete }: SyncProps) {
  const {
    syncing, logs, error, completed, currentPhase,
    checkpoint, paused, startSync, clearLogs, resumeSync,
  } = useSync(onSyncComplete);
  const logEndRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to bottom when new logs arrive.
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [logs]);

  const statsText = status?.graph_ready
    ? `${status.node_count.toLocaleString()} nodes, ${status.edge_count.toLocaleString()} edges` +
      (status.vector_count > 0 ? `, ${status.vector_count.toLocaleString()} vectors` : '')
    : 'No graph indexed yet';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={headerStyle}>
        <h2 style={titleStyle}>Sync Knowledge Graph</h2>
        <p style={subtitleStyle}>{statsText}</p>
      </div>

      <div style={buttonRow}>
        <button
          style={syncing ? { ...btnPrimary, ...btnDisabled } : btnPrimary}
          disabled={syncing}
          onClick={() => startSync(false)}
        >
          {syncing ? 'Syncing...' : 'Incremental Sync'}
        </button>
        <button
          style={syncing ? { ...btnSecondary, ...btnDisabled } : btnSecondary}
          disabled={syncing}
          onClick={() => startSync(true)}
        >
          Full Sync
        </button>
        {!syncing && logs.length > 0 && (
          <button style={btnSecondary} onClick={clearLogs}>
            Clear Log
          </button>
        )}
      </div>

      {currentPhase && syncing && <div style={phaseLabel}>{currentPhase}</div>}

      {error && <div style={errorBanner}>{error}</div>}
      {completed && !error && <div style={successBanner}>Sync completed successfully.</div>}

      {paused && checkpoint && (
        <div style={checkpointBanner}>
          <div style={checkpointRow}>
            <div>
              <strong>Face Checkpoint</strong> — {checkpoint.new_clusters} new face groups found.{' '}
              {checkpoint.images_processed}/{checkpoint.total_images} images processed.
            </div>
            <div style={{ display: 'flex', gap: '6px' }}>
              <button style={btnPrimary} onClick={resumeSync}>
                Continue Sync
              </button>
            </div>
          </div>
        </div>
      )}

      <div style={logContainer}>
        {logs.length === 0 && !syncing ? (
          <div style={emptyStyle}>
            Click "Incremental Sync" to update the knowledge graph with recent changes,
            or "Full Sync" to rebuild from scratch.
          </div>
        ) : (
          logs.map((entry, i) => (
            <div key={i}>
              <span style={timestampStyle}>[{entry.timestamp}]</span>
              {entry.message}
            </div>
          ))
        )}
        <div ref={logEndRef} />
      </div>
    </div>
  );
}
