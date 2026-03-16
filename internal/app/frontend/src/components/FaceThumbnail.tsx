import React from 'react';
import { useFaceThumbnail } from '../hooks/useFaceThumbnail';

interface FaceThumbnailProps {
  imagePath: string;
  faceIndex: number;
  size?: number;
  clusterID?: number;
  draggable?: boolean;
  onClick?: () => void;
  onRemove?: () => void;
}

const placeholderStyle = (size: number): React.CSSProperties => ({
  width: size,
  height: size,
  background: '#313244',
  borderRadius: '4px',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  color: '#585b70',
  fontSize: '10px',
});

const imgStyle = (size: number): React.CSSProperties => ({
  width: size,
  height: size,
  borderRadius: '4px',
  objectFit: 'cover',
  cursor: 'pointer',
});

const removeBtn: React.CSSProperties = {
  position: 'absolute',
  top: -4,
  right: -4,
  width: 16,
  height: 16,
  borderRadius: '50%',
  background: '#f38ba8',
  color: '#1e1e2e',
  border: 'none',
  fontSize: '10px',
  lineHeight: '16px',
  textAlign: 'center',
  cursor: 'pointer',
  padding: 0,
};

export default function FaceThumbnail({
  imagePath,
  faceIndex,
  size = 64,
  clusterID,
  draggable = false,
  onClick,
  onRemove,
}: FaceThumbnailProps) {
  const { src, loading } = useFaceThumbnail(imagePath, faceIndex, size);

  const handleDragStart = (e: React.DragEvent) => {
    e.dataTransfer.setData(
      'application/json',
      JSON.stringify({
        type: 'face',
        cluster_id: clusterID ?? -1,
        image_path: imagePath,
        face_index: faceIndex,
      })
    );
    e.dataTransfer.effectAllowed = 'move';
  };

  if (loading || !src) {
    return <div style={placeholderStyle(size)}>{loading ? '...' : '?'}</div>;
  }

  return (
    <div style={{ position: 'relative', display: 'inline-block' }}>
      <img
        src={src}
        alt={`Face ${faceIndex}`}
        style={imgStyle(size)}
        draggable={draggable}
        onDragStart={draggable ? handleDragStart : undefined}
        onClick={onClick}
      />
      {onRemove && (
        <button
          style={removeBtn}
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          title="Remove from cluster"
        >
          x
        </button>
      )}
    </div>
  );
}
