import React, { useCallback } from 'react';
import FaceThumbnail from './FaceThumbnail';
import type { MergeSuggestion, ClusterDetail } from '../types';

interface MergeSuggestionsProps {
  suggestions: MergeSuggestion[];
  clusters: ClusterDetail[];
  onMerge: (targetID: number, sourceIDs: number[]) => void;
  onDismiss: (index: number) => void;
}

const containerStyle: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: '8px',
};

const cardStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  padding: '10px 14px',
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: '6px',
};

const simStyle: React.CSSProperties = {
  fontSize: '14px',
  fontWeight: 700,
  color: '#a6e3a1',
  minWidth: '50px',
  textAlign: 'center',
};

const infoStyle: React.CSSProperties = {
  flex: 1,
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
};

const labelText: React.CSSProperties = {
  fontSize: '12px',
  color: '#cdd6f4',
};

const countText: React.CSSProperties = {
  fontSize: '11px',
  color: '#a6adc8',
};

const arrowStyle: React.CSSProperties = {
  fontSize: '14px',
  color: '#585b70',
};

const btnMerge: React.CSSProperties = {
  padding: '4px 10px',
  fontSize: '11px',
  fontWeight: 600,
  border: 'none',
  borderRadius: '4px',
  cursor: 'pointer',
  background: '#89b4fa',
  color: '#1e1e2e',
};

const btnDismiss: React.CSSProperties = {
  padding: '4px 10px',
  fontSize: '11px',
  fontWeight: 600,
  border: 'none',
  borderRadius: '4px',
  cursor: 'pointer',
  background: '#45475a',
  color: '#cdd6f4',
};

function getFirstFace(clusters: ClusterDetail[], clusterID: number) {
  const c = clusters.find((cl) => cl.cluster_id === clusterID);
  return c?.faces?.[0] ?? null;
}

export default function MergeSuggestions({
  suggestions,
  clusters,
  onMerge,
  onDismiss,
}: MergeSuggestionsProps) {
  const handleMerge = useCallback(
    (s: MergeSuggestion) => {
      // Merge into the larger cluster.
      if (s.face_count_a >= s.face_count_b) {
        onMerge(s.cluster_a, [s.cluster_b]);
      } else {
        onMerge(s.cluster_b, [s.cluster_a]);
      }
    },
    [onMerge]
  );

  if (suggestions.length === 0) {
    return (
      <div style={{ color: '#a6adc8', fontSize: '13px', padding: '16px 0' }}>
        No merge suggestions found.
      </div>
    );
  }

  return (
    <div style={containerStyle}>
      {suggestions.map((s, i) => {
        const faceA = getFirstFace(clusters, s.cluster_a);
        const faceB = getFirstFace(clusters, s.cluster_b);

        return (
          <div key={`${s.cluster_a}-${s.cluster_b}`} style={cardStyle}>
            <div style={simStyle}>{(s.similarity * 100).toFixed(0)}%</div>
            <div style={infoStyle}>
              {faceA && (
                <FaceThumbnail
                  imagePath={faceA.image_path}
                  faceIndex={faceA.face_index}
                  size={40}
                />
              )}
              <div>
                <div style={labelText}>{s.label_a || `#${s.cluster_a}`}</div>
                <div style={countText}>{s.face_count_a} faces</div>
              </div>
              <span style={arrowStyle}>&harr;</span>
              {faceB && (
                <FaceThumbnail
                  imagePath={faceB.image_path}
                  faceIndex={faceB.face_index}
                  size={40}
                />
              )}
              <div>
                <div style={labelText}>{s.label_b || `#${s.cluster_b}`}</div>
                <div style={countText}>{s.face_count_b} faces</div>
              </div>
            </div>
            <button style={btnMerge} onClick={() => handleMerge(s)}>
              Merge
            </button>
            <button style={btnDismiss} onClick={() => onDismiss(i)}>
              Skip
            </button>
          </div>
        );
      })}
    </div>
  );
}
