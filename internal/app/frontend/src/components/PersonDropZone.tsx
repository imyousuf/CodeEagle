import React, { useState, useCallback } from 'react';
import type { PersonInfo } from '../types';

interface PersonDropZoneProps {
  persons: PersonInfo[];
  onAssignCluster: (clusterID: number, personID: string) => void;
  onAssignFace: (personID: string, imagePath: string, faceIndex: number) => void;
}

const containerStyle: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: '6px',
};

const titleStyle: React.CSSProperties = {
  fontSize: '13px',
  fontWeight: 600,
  color: '#cdd6f4',
  marginBottom: '4px',
};

const personCard = (dragOver: boolean): React.CSSProperties => ({
  padding: '10px 12px',
  background: dragOver ? '#302040' : '#181825',
  border: `1px solid ${dragOver ? '#cba6f7' : '#313244'}`,
  borderRadius: '6px',
  transition: 'all 0.15s',
  cursor: 'default',
});

const nameStyle: React.CSSProperties = {
  fontSize: '13px',
  fontWeight: 600,
  color: '#cdd6f4',
};

const metaStyle: React.CSSProperties = {
  fontSize: '11px',
  color: '#a6adc8',
};

function PersonDropCard({
  person,
  onAssignCluster,
  onAssignFace,
}: {
  person: PersonInfo;
  onAssignCluster: (clusterID: number, personID: string) => void;
  onAssignFace: (personID: string, imagePath: string, faceIndex: number) => void;
}) {
  const [dragOver, setDragOver] = useState(false);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(true);
  }, []);

  const handleDragLeave = useCallback(() => {
    setDragOver(false);
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragOver(false);
      try {
        const data = JSON.parse(e.dataTransfer.getData('application/json'));
        if (data.type === 'cluster') {
          onAssignCluster(data.cluster_id, person.id);
        } else if (data.type === 'face') {
          onAssignFace(person.id, data.image_path, data.face_index);
        }
      } catch {
        // Ignore invalid drop data.
      }
    },
    [person.id, onAssignCluster, onAssignFace]
  );

  return (
    <div
      style={personCard(dragOver)}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div style={nameStyle}>{person.name}</div>
      <div style={metaStyle}>
        {person.relationships?.length > 0 && (
          <span>{person.relationships.join(', ')} &middot; </span>
        )}
        {person.face_count} face{person.face_count !== 1 ? 's' : ''}
      </div>
    </div>
  );
}

export default function PersonDropZone({
  persons,
  onAssignCluster,
  onAssignFace,
}: PersonDropZoneProps) {
  return (
    <div style={containerStyle}>
      <div style={titleStyle}>Drop on Person</div>
      {persons.length === 0 ? (
        <div style={{ fontSize: '12px', color: '#585b70' }}>
          No persons defined. Add one in the Overview tab.
        </div>
      ) : (
        persons.map((p) => (
          <PersonDropCard
            key={p.id}
            person={p}
            onAssignCluster={onAssignCluster}
            onAssignFace={onAssignFace}
          />
        ))
      )}
    </div>
  );
}
