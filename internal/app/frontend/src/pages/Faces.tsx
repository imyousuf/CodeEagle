import { useState, useCallback } from 'react';
import { useFaces } from '../hooks/useFaces';
import type { AppStatus } from '../types';

interface FacesProps {
  status: AppStatus | null;
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

const sectionTitle: React.CSSProperties = {
  fontSize: '15px',
  fontWeight: 600,
  color: '#cdd6f4',
  margin: '20px 0 10px 0',
};

const statsGrid: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))',
  gap: '10px',
  marginBottom: '20px',
};

const statCard: React.CSSProperties = {
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: '6px',
  padding: '12px',
  textAlign: 'center',
};

const statValue: React.CSSProperties = {
  fontSize: '22px',
  fontWeight: 700,
  color: '#89b4fa',
  margin: '0 0 4px 0',
};

const statLabel: React.CSSProperties = {
  fontSize: '11px',
  color: '#a6adc8',
  textTransform: 'uppercase',
  letterSpacing: '0.5px',
};

const personRow: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '10px 14px',
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: '6px',
  marginBottom: '6px',
};

const personName: React.CSSProperties = {
  fontSize: '14px',
  fontWeight: 600,
  color: '#cdd6f4',
};

const personMeta: React.CSSProperties = {
  fontSize: '12px',
  color: '#a6adc8',
  marginLeft: '8px',
};

const btnBase: React.CSSProperties = {
  padding: '6px 14px',
  fontSize: '12px',
  fontWeight: 600,
  border: 'none',
  borderRadius: '5px',
  cursor: 'pointer',
  transition: 'opacity 0.15s',
};

const btnPrimary: React.CSSProperties = {
  ...btnBase,
  background: '#89b4fa',
  color: '#1e1e2e',
};

const btnDanger: React.CSSProperties = {
  ...btnBase,
  background: '#f38ba8',
  color: '#1e1e2e',
  marginLeft: '6px',
};

const inputStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '5px',
  color: '#cdd6f4',
  outline: 'none',
  width: '200px',
};

const selectStyle: React.CSSProperties = {
  ...inputStyle,
  width: '160px',
};

const formRow: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  alignItems: 'center',
  marginBottom: '12px',
};

const emptyStyle: React.CSSProperties = {
  textAlign: 'center',
  padding: '32px 0',
  color: '#a6adc8',
  fontSize: '14px',
};

const checkpointBanner: React.CSSProperties = {
  background: '#302040',
  border: '1px solid #cba6f7',
  borderRadius: '6px',
  padding: '14px',
  marginBottom: '16px',
};

const checkpointTitle: React.CSSProperties = {
  fontSize: '14px',
  fontWeight: 600,
  color: '#cba6f7',
  margin: '0 0 8px 0',
};

const checkpointText: React.CSSProperties = {
  fontSize: '13px',
  color: '#a6adc8',
  margin: '0 0 10px 0',
};

const knownRelationships = ['family', 'friend', 'colleague', 'acquaintance', 'other'];

export default function Faces({ status }: FacesProps) {
  const {
    persons,
    stats,
    checkpoint,
    paused,
    loading,
    error,
    createPerson,
    deletePerson,
    resumeSync,
  } = useFaces();

  const [newName, setNewName] = useState('');
  const [newRel, setNewRel] = useState('');
  const [creating, setCreating] = useState(false);

  const handleCreate = useCallback(async () => {
    if (!newName.trim()) return;
    setCreating(true);
    try {
      await createPerson(newName.trim(), newRel ? [newRel] : []);
      setNewName('');
      setNewRel('');
    } catch {
      // Error handled by hook
    }
    setCreating(false);
  }, [newName, newRel, createPerson]);

  const handleDelete = useCallback(async (id: string, name: string) => {
    if (!confirm(`Delete "${name}" and all associated face data?`)) return;
    await deletePerson(id);
  }, [deletePerson]);

  if (!status?.graph_ready) {
    return (
      <div style={emptyStyle}>
        No knowledge graph available. Run a sync first to index your images.
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={headerStyle}>
        <h2 style={titleStyle}>Face Recognition</h2>
        <p style={subtitleStyle}>
          Manage recognized persons and review face detection results
        </p>
      </div>

      {/* Checkpoint banner */}
      {paused && checkpoint && (
        <div style={checkpointBanner}>
          <p style={checkpointTitle}>Face Checkpoint</p>
          <p style={checkpointText}>
            {checkpoint.new_clusters} new face groups found.{' '}
            {checkpoint.images_processed} of {checkpoint.total_images} images processed.
          </p>
          <div style={{ display: 'flex', gap: '8px' }}>
            <button style={btnPrimary} onClick={resumeSync}>
              Continue Sync
            </button>
          </div>
        </div>
      )}

      {error && (
        <div style={{ ...checkpointBanner, borderColor: '#f38ba8', background: '#302030' }}>
          <p style={{ ...checkpointTitle, color: '#f38ba8' }}>{error}</p>
        </div>
      )}

      {/* Stats */}
      {stats && (
        <div style={statsGrid}>
          <div style={statCard}>
            <div style={statValue}>{stats.total_persons}</div>
            <div style={statLabel}>Persons</div>
          </div>
          <div style={statCard}>
            <div style={statValue}>{stats.total_faces}</div>
            <div style={statLabel}>Faces</div>
          </div>
          <div style={statCard}>
            <div style={statValue}>{stats.total_images}</div>
            <div style={statLabel}>Images</div>
          </div>
          <div style={statCard}>
            <div style={statValue}>{stats.scanned_count}</div>
            <div style={statLabel}>Scanned</div>
          </div>
          {stats.oldest_date && (
            <div style={statCard}>
              <div style={{ ...statValue, fontSize: '14px' }}>{stats.oldest_date}</div>
              <div style={statLabel}>Oldest</div>
            </div>
          )}
          {stats.newest_date && (
            <div style={statCard}>
              <div style={{ ...statValue, fontSize: '14px' }}>{stats.newest_date}</div>
              <div style={statLabel}>Newest</div>
            </div>
          )}
        </div>
      )}

      {/* Add Person */}
      <h3 style={sectionTitle}>Add Person</h3>
      <div style={formRow}>
        <input
          type="text"
          placeholder="Person name"
          value={newName}
          onChange={e => setNewName(e.target.value)}
          style={inputStyle}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
        />
        <select
          value={newRel}
          onChange={e => setNewRel(e.target.value)}
          style={selectStyle}
        >
          <option value="">Relationship (optional)</option>
          {knownRelationships.map(r => (
            <option key={r} value={r}>{r}</option>
          ))}
        </select>
        <button
          style={creating || !newName.trim() ? { ...btnPrimary, opacity: 0.5 } : btnPrimary}
          disabled={creating || !newName.trim()}
          onClick={handleCreate}
        >
          Add
        </button>
      </div>

      {/* Persons List */}
      <h3 style={sectionTitle}>
        Known Persons ({persons.length})
      </h3>

      {loading && persons.length === 0 ? (
        <div style={emptyStyle}>Loading...</div>
      ) : persons.length === 0 ? (
        <div style={emptyStyle}>
          No persons defined yet. Add a person above to start face recognition.
        </div>
      ) : (
        <div style={{ overflow: 'auto', flex: 1 }}>
          {persons.map(p => (
            <div key={p.id} style={personRow}>
              <div style={{ display: 'flex', alignItems: 'center' }}>
                <span style={personName}>{p.name}</span>
                {p.relationships?.length > 0 && (
                  <span style={personMeta}>{p.relationships.join(', ')}</span>
                )}
                <span style={personMeta}>{p.face_count} face{p.face_count !== 1 ? 's' : ''}</span>
              </div>
              <div>
                <button
                  style={btnDanger}
                  onClick={() => handleDelete(p.id, p.name)}
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
