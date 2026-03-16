import React, { useState, useCallback } from 'react';
import FaceThumbnail from './FaceThumbnail';
import type { ClusterDetail } from '../types';

interface ClusterCardProps {
  cluster: ClusterDetail;
  selected?: boolean;
  onSelect?: (clusterID: number, multi: boolean) => void;
  onRemoveFace?: (imagePath: string, faceIndex: number) => void;
  onLabelChange?: (clusterID: number, label: string) => void;
  onFaceClick?: (imagePath: string, faceIndex: number) => void;
}

const cardStyle = (selected: boolean): React.CSSProperties => ({
  background: '#181825',
  border: `1px solid ${selected ? '#89b4fa' : '#313244'}`,
  borderRadius: '8px',
  padding: '12px',
  cursor: 'grab',
  transition: 'border-color 0.15s',
});

const headerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '8px',
};

const labelStyle: React.CSSProperties = {
  fontSize: '13px',
  fontWeight: 600,
  color: '#cdd6f4',
};

const countStyle: React.CSSProperties = {
  fontSize: '11px',
  color: '#a6adc8',
};

const gridStyle: React.CSSProperties = {
  display: 'flex',
  flexWrap: 'wrap',
  gap: '4px',
};

const editInput: React.CSSProperties = {
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '4px',
  color: '#cdd6f4',
  fontSize: '12px',
  padding: '2px 6px',
  width: '120px',
  outline: 'none',
};

const PREVIEW_COUNT = 6;

export default function ClusterCard({
  cluster,
  selected = false,
  onSelect,
  onRemoveFace,
  onLabelChange,
  onFaceClick,
}: ClusterCardProps) {
  const [expanded, setExpanded] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState(cluster.label);

  const handleDragStart = useCallback(
    (e: React.DragEvent) => {
      e.dataTransfer.setData(
        'application/json',
        JSON.stringify({
          type: 'cluster',
          cluster_id: cluster.cluster_id,
        })
      );
      e.dataTransfer.effectAllowed = 'move';
    },
    [cluster.cluster_id]
  );

  const handleClick = useCallback(
    (e: React.MouseEvent) => {
      if (onSelect) {
        onSelect(cluster.cluster_id, e.shiftKey);
      }
    },
    [cluster.cluster_id, onSelect]
  );

  const handleLabelSave = useCallback(() => {
    setEditing(false);
    if (editValue !== cluster.label && onLabelChange) {
      onLabelChange(cluster.cluster_id, editValue);
    }
  }, [editValue, cluster.label, cluster.cluster_id, onLabelChange]);

  const facesToShow = expanded ? cluster.faces : cluster.faces.slice(0, PREVIEW_COUNT);
  const hasMore = cluster.faces.length > PREVIEW_COUNT;

  return (
    <div
      style={cardStyle(selected)}
      draggable
      onDragStart={handleDragStart}
      onClick={handleClick}
    >
      <div style={headerStyle}>
        <div>
          {editing ? (
            <input
              style={editInput}
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={handleLabelSave}
              onKeyDown={(e) => e.key === 'Enter' && handleLabelSave()}
              autoFocus
              onClick={(e) => e.stopPropagation()}
            />
          ) : (
            <span
              style={labelStyle}
              onDoubleClick={(e) => {
                e.stopPropagation();
                setEditing(true);
                setEditValue(cluster.label);
              }}
              title="Double-click to edit label"
            >
              {cluster.label || `Cluster #${cluster.cluster_id}`}
            </span>
          )}
        </div>
        <span style={countStyle}>
          {cluster.face_count} face{cluster.face_count !== 1 ? 's' : ''}
        </span>
      </div>

      <div style={gridStyle}>
        {facesToShow.map((f) => (
          <FaceThumbnail
            key={`${f.image_path}:${f.face_index}`}
            imagePath={f.image_path}
            faceIndex={f.face_index}
            size={48}
            clusterID={cluster.cluster_id}
            draggable
            onClick={() => onFaceClick?.(f.image_path, f.face_index)}
            onRemove={
              onRemoveFace
                ? () => onRemoveFace(f.image_path, f.face_index)
                : undefined
            }
          />
        ))}
      </div>

      {hasMore && !expanded && (
        <button
          style={{
            background: 'none',
            border: 'none',
            color: '#89b4fa',
            fontSize: '11px',
            cursor: 'pointer',
            marginTop: '6px',
            padding: 0,
          }}
          onClick={(e) => {
            e.stopPropagation();
            setExpanded(true);
          }}
        >
          Show all {cluster.faces.length} faces
        </button>
      )}
      {expanded && hasMore && (
        <button
          style={{
            background: 'none',
            border: 'none',
            color: '#89b4fa',
            fontSize: '11px',
            cursor: 'pointer',
            marginTop: '6px',
            padding: 0,
          }}
          onClick={(e) => {
            e.stopPropagation();
            setExpanded(false);
          }}
        >
          Show less
        </button>
      )}
    </div>
  );
}
