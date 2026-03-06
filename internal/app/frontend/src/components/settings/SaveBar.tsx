import { useState } from 'react';
import type { ConfigDiff } from '../../types';

interface Props {
  isDirty: boolean;
  onSave: () => void;
  onReset: () => void;
  onPreview: () => Promise<ConfigDiff[]>;
  saving: boolean;
  message?: string | null;
}

const barStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '10px 16px',
  background: '#181825',
  borderTop: '1px solid #45475a',
  flexShrink: 0,
};

const leftStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
};

const rightStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
};

const btnPrimary: React.CSSProperties = {
  padding: '6px 16px',
  fontSize: '13px',
  background: '#89b4fa',
  border: 'none',
  borderRadius: '6px',
  color: '#1e1e2e',
  fontWeight: 600,
  cursor: 'pointer',
};

const btnSecondary: React.CSSProperties = {
  padding: '6px 16px',
  fontSize: '13px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#cdd6f4',
  cursor: 'pointer',
};

const msgStyle = (isError: boolean): React.CSSProperties => ({
  fontSize: '12px',
  color: isError ? '#f38ba8' : '#a6e3a1',
});

const previewStyle: React.CSSProperties = {
  position: 'fixed',
  top: 0,
  left: 0,
  right: 0,
  bottom: 0,
  background: 'rgba(0,0,0,0.7)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 100,
};

const previewBoxStyle: React.CSSProperties = {
  background: '#1e1e2e',
  border: '1px solid #45475a',
  borderRadius: '12px',
  padding: '20px',
  maxWidth: '600px',
  width: '90%',
  maxHeight: '70vh',
  overflow: 'auto',
};

const tableStyle: React.CSSProperties = {
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: '13px',
};

const thStyle: React.CSSProperties = {
  textAlign: 'left',
  padding: '6px 8px',
  borderBottom: '1px solid #45475a',
  color: '#89b4fa',
  fontWeight: 600,
};

const tdStyle: React.CSSProperties = {
  padding: '6px 8px',
  borderBottom: '1px solid #313244',
  color: '#cdd6f4',
};

export default function SaveBar({ isDirty, onSave, onReset, onPreview, saving, message }: Props) {
  const [showPreview, setShowPreview] = useState(false);
  const [diffs, setDiffs] = useState<ConfigDiff[]>([]);

  const handlePreview = async () => {
    const result = await onPreview();
    setDiffs(result);
    setShowPreview(true);
  };

  const isError = message ? !message.toLowerCase().includes('saved') : false;

  return (
    <>
      <div style={barStyle}>
        <div style={leftStyle}>
          {message && <span style={msgStyle(isError)}>{message}</span>}
          {isDirty && !message && (
            <span style={{ fontSize: '12px', color: '#f9e2af' }}>Unsaved changes</span>
          )}
        </div>
        <div style={rightStyle}>
          {isDirty && (
            <>
              <button type="button" style={btnSecondary} onClick={handlePreview}>
                Preview
              </button>
              <button type="button" style={btnSecondary} onClick={onReset}>
                Reset
              </button>
              <button
                type="button"
                style={{ ...btnPrimary, opacity: saving ? 0.6 : 1 }}
                onClick={onSave}
                disabled={saving}
              >
                {saving ? 'Saving...' : 'Save'}
              </button>
            </>
          )}
        </div>
      </div>

      {showPreview && (
        <div style={previewStyle} onClick={() => setShowPreview(false)}>
          <div style={previewBoxStyle} onClick={e => e.stopPropagation()}>
            <h3 style={{ margin: '0 0 12px', color: '#cdd6f4', fontSize: '16px' }}>
              Changes Preview
            </h3>
            {diffs.length === 0 ? (
              <p style={{ color: '#6c7086' }}>No changes detected.</p>
            ) : (
              <table style={tableStyle}>
                <thead>
                  <tr>
                    <th style={thStyle}>Section</th>
                    <th style={thStyle}>Field</th>
                    <th style={thStyle}>Before</th>
                    <th style={thStyle}>After</th>
                  </tr>
                </thead>
                <tbody>
                  {diffs.map((d, i) => (
                    <tr key={i}>
                      <td style={tdStyle}>{d.section}</td>
                      <td style={tdStyle}>{d.field}</td>
                      <td style={{ ...tdStyle, color: '#f38ba8' }}>{d.old_value || '(empty)'}</td>
                      <td style={{ ...tdStyle, color: '#a6e3a1' }}>{d.new_value || '(empty)'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <div style={{ marginTop: '16px', textAlign: 'right' }}>
              <button type="button" style={btnSecondary} onClick={() => setShowPreview(false)}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
