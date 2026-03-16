import { useState, useCallback } from 'react';
import { useFaces } from '../hooks/useFaces';
import { useClusters } from '../hooks/useClusters';
import Combobox from '../components/Combobox';
import ClusterGrid from '../components/ClusterGrid';
import PersonDropZone from '../components/PersonDropZone';
import MergeSuggestions from '../components/MergeSuggestions';
import ImagePreviewModal from '../components/ImagePreviewModal';
import FaceThumbnail from '../components/FaceThumbnail';
import type { AppStatus } from '../types';

interface FacesProps {
  status: AppStatus | null;
}

type Tab = 'overview' | 'clusters' | 'noise';

// ---------------------------------------------------------------------------
// Shared styles
// ---------------------------------------------------------------------------

const headerStyle: React.CSSProperties = {
  marginBottom: '8px',
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

const btnSecondary: React.CSSProperties = {
  ...btnBase,
  background: '#45475a',
  color: '#cdd6f4',
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

// Tab styles
const tabBar: React.CSSProperties = {
  display: 'flex',
  gap: '0',
  borderBottom: '1px solid #313244',
  marginBottom: '16px',
};

const tabBtn = (active: boolean): React.CSSProperties => ({
  padding: '8px 16px',
  fontSize: '13px',
  fontWeight: 600,
  color: active ? '#89b4fa' : '#a6adc8',
  background: 'none',
  border: 'none',
  borderBottom: active ? '2px solid #89b4fa' : '2px solid transparent',
  cursor: 'pointer',
  transition: 'color 0.15s, border-color 0.15s',
});

const knownRelationships = [
  'son', 'daughter', 'father', 'mother', 'brother', 'sister',
  'husband', 'wife', 'spouse',
  'father-in-law', 'mother-in-law', 'brother-in-law', 'sister-in-law', 'son-in-law', 'daughter-in-law',
  'grandfather', 'grandmother', 'grandson', 'granddaughter', 'uncle', 'aunt', 'nephew', 'niece', 'cousin',
  'friend', 'colleague', 'neighbor', 'other',
];

export default function Faces({ status }: FacesProps) {
  const {
    persons,
    stats,
    checkpoint,
    paused,
    loading,
    error,
    refresh: refreshFaces,
    createPerson,
    deletePerson,
    resumeSync,
  } = useFaces();

  const {
    clusters,
    noiseFaces,
    suggestions,
    clusteringInProgress,
    clusteringProgress,
    error: clusterError,
    refresh: refreshClusters,
    runClustering,
    removeFace,
    mergeClusters,
    setLabel,
    assignToPerson,
    loadSuggestions,
  } = useClusters();

  const [activeTab, setActiveTab] = useState<Tab>('overview');
  const [newName, setNewName] = useState('');
  const [newRel, setNewRel] = useState('');
  const [creating, setCreating] = useState(false);
  const [simThreshold, setSimThreshold] = useState('0.30');
  const [selectedClusters, setSelectedClusters] = useState<Set<number>>(new Set());
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [previewFace, setPreviewFace] = useState<{ path: string; idx: number } | null>(null);
  const [dismissedSuggestions, setDismissedSuggestions] = useState<Set<number>>(new Set());

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

  const handleSelectCluster = useCallback((clusterID: number, multi: boolean) => {
    setSelectedClusters(prev => {
      const next = new Set(multi ? prev : []);
      if (next.has(clusterID)) {
        next.delete(clusterID);
      } else {
        next.add(clusterID);
      }
      return next;
    });
  }, []);

  const handleMergeSelected = useCallback(async () => {
    const ids = Array.from(selectedClusters);
    if (ids.length < 2) return;
    await mergeClusters(ids[0], ids.slice(1));
    setSelectedClusters(new Set());
  }, [selectedClusters, mergeClusters]);

  const handleRunClustering = useCallback(async () => {
    const thresh = parseFloat(simThreshold);
    if (isNaN(thresh) || thresh <= 0) return;
    await runClustering(thresh);
  }, [simThreshold, runClustering]);

  const handleAssignCluster = useCallback(async (clusterID: number, personID: string) => {
    await assignToPerson(clusterID, personID);
    refreshFaces();
  }, [assignToPerson, refreshFaces]);

  const handleAssignFace = useCallback(async (personID: string, imagePath: string, faceIndex: number) => {
    await window.go.app.App.AssignFaceToPerson(personID, imagePath, faceIndex, 1.0);
    refreshFaces();
    refreshClusters();
  }, [refreshFaces, refreshClusters]);

  const handleShowSuggestions = useCallback(async () => {
    await loadSuggestions();
    setShowSuggestions(true);
    setDismissedSuggestions(new Set());
  }, [loadSuggestions]);

  const handleDismissSuggestion = useCallback((index: number) => {
    setDismissedSuggestions(prev => new Set(prev).add(index));
  }, []);

  const handleMergeSuggestion = useCallback(async (targetID: number, sourceIDs: number[]) => {
    await mergeClusters(targetID, sourceIDs);
  }, [mergeClusters]);

  const handleFaceClick = useCallback((imagePath: string, faceIndex: number) => {
    setPreviewFace({ path: imagePath, idx: faceIndex });
  }, []);

  if (!status?.graph_ready) {
    return (
      <div style={emptyStyle}>
        No knowledge graph available. Run a sync first to index your images.
      </div>
    );
  }

  const visibleSuggestions = suggestions.filter((_, i) => !dismissedSuggestions.has(i));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={headerStyle}>
        <h2 style={titleStyle}>Face Recognition</h2>
        <p style={subtitleStyle}>
          Manage persons, cluster faces, and associate them
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

      {(error || clusterError) && (
        <div style={{ ...checkpointBanner, borderColor: '#f38ba8', background: '#302030' }}>
          <p style={{ ...checkpointTitle, color: '#f38ba8' }}>{error || clusterError}</p>
        </div>
      )}

      {/* Tab bar */}
      <div style={tabBar}>
        <button style={tabBtn(activeTab === 'overview')} onClick={() => setActiveTab('overview')}>
          Overview
        </button>
        <button style={tabBtn(activeTab === 'clusters')} onClick={() => setActiveTab('clusters')}>
          Clusters ({clusters.length})
        </button>
        <button style={tabBtn(activeTab === 'noise')} onClick={() => setActiveTab('noise')}>
          Noise ({noiseFaces.length})
        </button>
      </div>

      {/* ================================================================= */}
      {/* Overview Tab */}
      {/* ================================================================= */}
      {activeTab === 'overview' && (
        <div style={{ overflow: 'auto', flex: 1 }}>
          {/* Stats */}
          {stats && (
            <div style={statsGrid}>
              <div style={statCard}>
                <div style={statValue}>{stats.total_persons}</div>
                <div style={statLabel}>Persons</div>
              </div>
              <div style={statCard}>
                <div style={statValue}>{stats.detected_faces}</div>
                <div style={statLabel}>Faces Detected</div>
              </div>
              <div style={statCard}>
                <div style={statValue}>{stats.images_with_faces}</div>
                <div style={statLabel}>With Faces</div>
              </div>
              <div style={statCard}>
                <div style={statValue}>{stats.total_faces}</div>
                <div style={statLabel}>Assigned</div>
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
            <Combobox
              options={knownRelationships}
              value={newRel}
              onChange={setNewRel}
              placeholder="Relationship (optional)"
              style={selectStyle}
            />
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
            <div>
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
      )}

      {/* ================================================================= */}
      {/* Clusters Tab */}
      {/* ================================================================= */}
      {activeTab === 'clusters' && (
        <div style={{ display: 'flex', gap: '16px', flex: 1, overflow: 'hidden' }}>
          {/* Main cluster area */}
          <div style={{ flex: 1, overflow: 'auto' }}>
            {/* Toolbar */}
            <div style={{ ...formRow, marginBottom: '16px' }}>
              <input
                type="number"
                step="0.05"
                min="0.1"
                max="1.0"
                value={simThreshold}
                onChange={e => setSimThreshold(e.target.value)}
                style={{ ...inputStyle, width: '80px' }}
                title="Similarity threshold"
              />
              <button
                style={clusteringInProgress ? { ...btnPrimary, opacity: 0.5 } : btnPrimary}
                disabled={clusteringInProgress}
                onClick={handleRunClustering}
              >
                {clusteringInProgress ? 'Clustering...' : 'Run Clustering'}
              </button>
              <button style={btnSecondary} onClick={handleShowSuggestions}>
                Suggest Merges
              </button>
              {selectedClusters.size >= 2 && (
                <button style={btnPrimary} onClick={handleMergeSelected}>
                  Merge Selected ({selectedClusters.size})
                </button>
              )}
            </div>

            {/* Clustering progress indicator */}
            {clusteringInProgress && (
              <div style={{
                background: '#181825',
                border: '1px solid #313244',
                borderRadius: '6px',
                padding: '12px 16px',
                marginBottom: '16px',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: clusteringProgress?.total ? '8px' : '0' }}>
                  <div style={{
                    width: '14px',
                    height: '14px',
                    border: '2px solid #89b4fa',
                    borderTopColor: 'transparent',
                    borderRadius: '50%',
                    animation: 'spin 1s linear infinite',
                  }} />
                  <span style={{ fontSize: '13px', color: '#cdd6f4', fontWeight: 600 }}>
                    {clusteringProgress?.phase || 'Starting clustering...'}
                  </span>
                  {clusteringProgress?.total ? (
                    <span style={{ fontSize: '12px', color: '#a6adc8' }}>
                      {clusteringProgress.current.toLocaleString()} / {clusteringProgress.total.toLocaleString()}
                    </span>
                  ) : null}
                </div>
                {clusteringProgress?.total ? (
                  <div style={{
                    height: '4px',
                    background: '#313244',
                    borderRadius: '2px',
                    overflow: 'hidden',
                  }}>
                    <div style={{
                      height: '100%',
                      width: `${Math.round((clusteringProgress.current / clusteringProgress.total) * 100)}%`,
                      background: '#89b4fa',
                      borderRadius: '2px',
                      transition: 'width 0.3s ease',
                    }} />
                  </div>
                ) : null}
              </div>
            )}

            {/* Merge suggestions panel */}
            {showSuggestions && (
              <div style={{ marginBottom: '16px' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '8px' }}>
                  <h3 style={{ ...sectionTitle, margin: 0 }}>Merge Suggestions</h3>
                  <button
                    style={{ ...btnSecondary, fontSize: '11px', padding: '3px 8px' }}
                    onClick={() => setShowSuggestions(false)}
                  >
                    Hide
                  </button>
                </div>
                <MergeSuggestions
                  suggestions={visibleSuggestions}
                  clusters={clusters}
                  onMerge={handleMergeSuggestion}
                  onDismiss={handleDismissSuggestion}
                />
              </div>
            )}

            {/* Cluster grid */}
            <ClusterGrid
              clusters={clusters}
              selectedClusters={selectedClusters}
              onSelect={handleSelectCluster}
              onRemoveFace={removeFace}
              onLabelChange={setLabel}
              onFaceClick={handleFaceClick}
            />
          </div>

          {/* Person drop zone sidebar */}
          <div style={{ width: '200px', flexShrink: 0, overflow: 'auto' }}>
            <PersonDropZone
              persons={persons}
              onAssignCluster={handleAssignCluster}
              onAssignFace={handleAssignFace}
            />
          </div>
        </div>
      )}

      {/* ================================================================= */}
      {/* Noise Tab */}
      {/* ================================================================= */}
      {activeTab === 'noise' && (
        <div style={{ overflow: 'auto', flex: 1 }}>
          <div style={{ marginBottom: '12px' }}>
            <span style={{ fontSize: '13px', color: '#a6adc8' }}>
              {noiseFaces.length} unassigned face{noiseFaces.length !== 1 ? 's' : ''} (not in any cluster)
            </span>
          </div>
          {noiseFaces.length === 0 ? (
            <div style={emptyStyle}>
              No noise faces. All detected faces are assigned to clusters.
            </div>
          ) : (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
              {noiseFaces.map((f) => (
                <div
                  key={`${f.image_path}:${f.face_index}`}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    gap: '2px',
                  }}
                >
                  <FaceThumbnail
                    imagePath={f.image_path}
                    faceIndex={f.face_index}
                    size={56}
                    draggable
                    onClick={() => handleFaceClick(f.image_path, f.face_index)}
                  />
                  <span style={{ fontSize: '9px', color: '#585b70', maxWidth: '56px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    #{f.face_index}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Image preview modal */}
      {previewFace && (
        <ImagePreviewModal
          imagePath={previewFace.path}
          faceIndex={previewFace.idx}
          onClose={() => setPreviewFace(null)}
        />
      )}
    </div>
  );
}
