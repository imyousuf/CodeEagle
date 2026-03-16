import React, { useState, useMemo } from 'react';
import ClusterCard from './ClusterCard';
import type { ClusterDetail } from '../types';

interface ClusterGridProps {
  clusters: ClusterDetail[];
  selectedClusters: Set<number>;
  onSelect: (clusterID: number, multi: boolean) => void;
  onRemoveFace: (imagePath: string, faceIndex: number) => void;
  onLabelChange: (clusterID: number, label: string) => void;
  onFaceClick: (imagePath: string, faceIndex: number) => void;
}

type SortKey = 'face_count' | 'cluster_id' | 'label';
type FilterKey = 'all' | 'labeled' | 'unlabeled';

const toolbarStyle: React.CSSProperties = {
  display: 'flex',
  gap: '8px',
  alignItems: 'center',
  marginBottom: '12px',
  flexWrap: 'wrap',
};

const selectStyle: React.CSSProperties = {
  padding: '4px 8px',
  fontSize: '12px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '4px',
  color: '#cdd6f4',
  outline: 'none',
};

const searchInput: React.CSSProperties = {
  padding: '4px 8px',
  fontSize: '12px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '4px',
  color: '#cdd6f4',
  outline: 'none',
  width: '160px',
};

const gridStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: '10px',
};

const labelStyle: React.CSSProperties = {
  fontSize: '11px',
  color: '#a6adc8',
};

export default function ClusterGrid({
  clusters,
  selectedClusters,
  onSelect,
  onRemoveFace,
  onLabelChange,
  onFaceClick,
}: ClusterGridProps) {
  const [sortBy, setSortBy] = useState<SortKey>('face_count');
  const [filterBy, setFilterBy] = useState<FilterKey>('all');
  const [search, setSearch] = useState('');

  const filtered = useMemo(() => {
    let result = [...clusters];

    // Filter.
    if (filterBy === 'labeled') {
      result = result.filter((c) => c.label);
    } else if (filterBy === 'unlabeled') {
      result = result.filter((c) => !c.label);
    }

    // Search.
    if (search) {
      const q = search.toLowerCase();
      result = result.filter(
        (c) =>
          c.label.toLowerCase().includes(q) ||
          `cluster #${c.cluster_id}`.includes(q)
      );
    }

    // Sort.
    result.sort((a, b) => {
      if (sortBy === 'face_count') return b.face_count - a.face_count;
      if (sortBy === 'cluster_id') return a.cluster_id - b.cluster_id;
      return a.label.localeCompare(b.label);
    });

    return result;
  }, [clusters, sortBy, filterBy, search]);

  return (
    <div>
      <div style={toolbarStyle}>
        <input
          style={searchInput}
          type="text"
          placeholder="Search clusters..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <label style={labelStyle}>
          Sort:
          <select
            style={{ ...selectStyle, marginLeft: 4 }}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as SortKey)}
          >
            <option value="face_count">Face Count</option>
            <option value="cluster_id">Cluster ID</option>
            <option value="label">Label</option>
          </select>
        </label>
        <label style={labelStyle}>
          Filter:
          <select
            style={{ ...selectStyle, marginLeft: 4 }}
            value={filterBy}
            onChange={(e) => setFilterBy(e.target.value as FilterKey)}
          >
            <option value="all">All</option>
            <option value="labeled">Labeled</option>
            <option value="unlabeled">Unlabeled</option>
          </select>
        </label>
        <span style={{ ...labelStyle, marginLeft: 'auto' }}>
          {filtered.length} cluster{filtered.length !== 1 ? 's' : ''}
        </span>
      </div>

      <div style={gridStyle}>
        {filtered.map((c) => (
          <ClusterCard
            key={c.cluster_id}
            cluster={c}
            selected={selectedClusters.has(c.cluster_id)}
            onSelect={onSelect}
            onRemoveFace={onRemoveFace}
            onLabelChange={onLabelChange}
            onFaceClick={onFaceClick}
          />
        ))}
      </div>

      {filtered.length === 0 && (
        <div
          style={{
            textAlign: 'center',
            padding: '32px 0',
            color: '#a6adc8',
            fontSize: '14px',
          }}
        >
          {clusters.length === 0
            ? 'No clusters yet. Run clustering to group detected faces.'
            : 'No clusters match the current filter.'}
        </div>
      )}
    </div>
  );
}
