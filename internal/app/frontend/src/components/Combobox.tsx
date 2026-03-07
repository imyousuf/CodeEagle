import { useState, useRef, useEffect, useCallback } from 'react';

interface ComboboxProps {
  options: string[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  style?: React.CSSProperties;
}

const dropdownStyle: React.CSSProperties = {
  position: 'absolute',
  top: '100%',
  left: 0,
  right: 0,
  marginTop: '2px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '5px',
  boxShadow: '0 4px 12px rgba(0,0,0,0.4)',
  maxHeight: '200px',
  overflowY: 'auto',
  zIndex: 100,
};

const itemStyle: React.CSSProperties = {
  padding: '6px 10px',
  fontSize: '13px',
  color: '#cdd6f4',
  cursor: 'pointer',
};

const itemHighlightedStyle: React.CSSProperties = {
  ...itemStyle,
  background: '#45475a',
};

export default function Combobox({ options, value, onChange, placeholder, style }: ComboboxProps) {
  const [open, setOpen] = useState(false);
  const [highlightIdx, setHighlightIdx] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered = options.filter(o =>
    o.toLowerCase().includes(value.toLowerCase())
  );

  const handleFocus = useCallback(() => setOpen(true), []);

  const handleBlur = useCallback((e: React.FocusEvent) => {
    // Don't close if clicking within the container (dropdown items).
    if (containerRef.current?.contains(e.relatedTarget as Node)) return;
    setOpen(false);
    setHighlightIdx(-1);
  }, []);

  const handleSelect = useCallback((val: string) => {
    onChange(val);
    setOpen(false);
    setHighlightIdx(-1);
  }, [onChange]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (!open) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        setOpen(true);
        e.preventDefault();
      }
      return;
    }

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setHighlightIdx(prev => (prev < filtered.length - 1 ? prev + 1 : 0));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setHighlightIdx(prev => (prev > 0 ? prev - 1 : filtered.length - 1));
        break;
      case 'Enter':
        e.preventDefault();
        if (highlightIdx >= 0 && highlightIdx < filtered.length) {
          handleSelect(filtered[highlightIdx]);
        }
        break;
      case 'Escape':
        e.preventDefault();
        setOpen(false);
        setHighlightIdx(-1);
        break;
    }
  }, [open, filtered, highlightIdx, handleSelect]);

  // Scroll highlighted item into view.
  useEffect(() => {
    if (highlightIdx >= 0 && listRef.current) {
      const item = listRef.current.children[highlightIdx] as HTMLElement;
      item?.scrollIntoView({ block: 'nearest' });
    }
  }, [highlightIdx]);

  // Reset highlight when filter changes.
  useEffect(() => {
    setHighlightIdx(-1);
  }, [value]);

  return (
    <div ref={containerRef} style={{ position: 'relative', display: 'inline-block' }}>
      <input
        type="text"
        value={value}
        onChange={e => { onChange(e.target.value); setOpen(true); }}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        style={style}
      />
      {open && filtered.length > 0 && (
        <div ref={listRef} style={dropdownStyle}>
          {filtered.map((opt, i) => (
            <div
              key={opt}
              tabIndex={-1}
              style={i === highlightIdx ? itemHighlightedStyle : itemStyle}
              onMouseDown={e => { e.preventDefault(); handleSelect(opt); }}
              onMouseEnter={() => setHighlightIdx(i)}
            >
              {opt}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
