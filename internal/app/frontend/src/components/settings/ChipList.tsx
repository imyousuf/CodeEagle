import { useState } from 'react';

interface Props {
  items: string[];
  onAdd: (item: string) => void;
  onRemove: (index: number) => void;
  placeholder?: string;
}

const containerStyle: React.CSSProperties = {
  display: 'flex',
  flexWrap: 'wrap',
  gap: '6px',
  marginBottom: '8px',
};

const chipStyle: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  padding: '3px 8px',
  fontSize: '12px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '12px',
  color: '#cdd6f4',
};

const removeBtnStyle: React.CSSProperties = {
  background: 'none',
  border: 'none',
  color: '#6c7086',
  cursor: 'pointer',
  fontSize: '14px',
  padding: '0',
  lineHeight: 1,
};

const inputRowStyle: React.CSSProperties = {
  display: 'flex',
  gap: '6px',
};

const inputStyle: React.CSSProperties = {
  flex: 1,
  padding: '5px 10px',
  fontSize: '12px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '6px',
  color: '#cdd6f4',
  outline: 'none',
};

const addBtnStyle: React.CSSProperties = {
  padding: '5px 12px',
  fontSize: '12px',
  background: '#45475a',
  border: 'none',
  borderRadius: '6px',
  color: '#cdd6f4',
  cursor: 'pointer',
};

export default function ChipList({ items, onAdd, onRemove, placeholder }: Props) {
  const [input, setInput] = useState('');

  const handleAdd = () => {
    if (input.trim()) {
      onAdd(input.trim());
      setInput('');
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleAdd();
    }
  };

  return (
    <div>
      {items.length > 0 && (
        <div style={containerStyle}>
          {items.map((item, i) => (
            <span key={i} style={chipStyle}>
              {item}
              <button type="button" style={removeBtnStyle} onClick={() => onRemove(i)}>
                &times;
              </button>
            </span>
          ))}
        </div>
      )}
      <div style={inputRowStyle}>
        <input
          type="text"
          style={inputStyle}
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder || 'Add item...'}
        />
        <button type="button" style={addBtnStyle} onClick={handleAdd}>
          Add
        </button>
      </div>
    </div>
  );
}
