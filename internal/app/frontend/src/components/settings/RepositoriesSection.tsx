import { useState, useEffect } from 'react';
import FieldGroup from './FieldGroup';
import PathPicker from './PathPicker';
import type { RepositoryDTO, ServiceStatus } from '../../types';

interface Props {
  repositories: RepositoryDTO[];
  onUpdate: (index: number, field: 'path' | 'type', value: string) => void;
  onAdd: () => void;
  onRemove: (index: number) => void;
  onBrowse: (title: string) => Promise<string>;
  onValidate: (path: string, expectDir: boolean) => Promise<ServiceStatus>;
}

const rowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'flex-start',
  gap: '8px',
  marginBottom: '10px',
};

const pathCol: React.CSSProperties = {
  flex: 1,
};

const selectStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
};

const removeBtnStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#f38ba8',
  cursor: 'pointer',
};

const addBtnStyle: React.CSSProperties = {
  padding: '6px 14px',
  fontSize: '13px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#a6e3a1',
  cursor: 'pointer',
};

export default function RepositoriesSection({
  repositories,
  onUpdate,
  onAdd,
  onRemove,
  onBrowse,
  onValidate,
}: Props) {
  const [pathStatus, setPathStatus] = useState<Record<number, ServiceStatus>>({});

  // Validate paths on change.
  useEffect(() => {
    repositories.forEach((repo, i) => {
      if (repo.path) {
        onValidate(repo.path, true).then(status => {
          setPathStatus(prev => ({ ...prev, [i]: status }));
        });
      }
    });
  }, [repositories, onValidate]);

  const handleBrowse = async (index: number) => {
    const dir = await onBrowse('Select repository directory');
    if (dir) {
      onUpdate(index, 'path', dir);
    }
  };

  return (
    <FieldGroup
      label="Repositories"
      description="Folders containing code you want CodeEagle to understand."
    >
      {repositories.map((repo, i) => (
        <div key={i} style={rowStyle}>
          <div style={pathCol}>
            <PathPicker
              value={repo.path}
              onChange={v => onUpdate(i, 'path', v)}
              onBrowse={() => handleBrowse(i)}
              placeholder="/path/to/your/code"
              status={pathStatus[i] || null}
            />
          </div>
          <select
            style={selectStyle}
            value={repo.type || 'single'}
            onChange={e => onUpdate(i, 'type', e.target.value)}
          >
            <option value="single">Single</option>
            <option value="monorepo">Monorepo</option>
          </select>
          {repositories.length > 1 && (
            <button type="button" style={removeBtnStyle} onClick={() => onRemove(i)}>
              Remove
            </button>
          )}
        </div>
      ))}
      <button type="button" style={addBtnStyle} onClick={onAdd}>
        + Add Repository
      </button>
    </FieldGroup>
  );
}
