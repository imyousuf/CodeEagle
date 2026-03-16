import React, { useState, useEffect, useCallback } from 'react';

interface ImagePreviewModalProps {
  imagePath: string;
  faceIndex: number;
  onClose: () => void;
}

const overlayStyle: React.CSSProperties = {
  position: 'fixed',
  top: 0,
  left: 0,
  right: 0,
  bottom: 0,
  background: 'rgba(0, 0, 0, 0.85)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 1000,
};

const contentStyle: React.CSSProperties = {
  maxWidth: '90vw',
  maxHeight: '90vh',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
};

const imgStyle: React.CSSProperties = {
  maxWidth: '100%',
  maxHeight: '80vh',
  borderRadius: '8px',
};

const metaStyle: React.CSSProperties = {
  marginTop: '12px',
  fontSize: '12px',
  color: '#a6adc8',
  textAlign: 'center',
  maxWidth: '600px',
  wordBreak: 'break-all',
};

const closeBtn: React.CSSProperties = {
  position: 'absolute',
  top: '16px',
  right: '16px',
  background: '#313244',
  border: '1px solid #45475a',
  borderRadius: '50%',
  width: '32px',
  height: '32px',
  color: '#cdd6f4',
  fontSize: '16px',
  cursor: 'pointer',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
};

export default function ImagePreviewModal({
  imagePath,
  faceIndex,
  onClose,
}: ImagePreviewModalProps) {
  const [src, setSrc] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    window.go?.app?.App?.GetImagePreview?.(imagePath, 1200)
      .then((data) => {
        if (data) setSrc(data);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [imagePath]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    },
    [onClose]
  );

  useEffect(() => {
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);

  return (
    <div style={overlayStyle} onClick={onClose}>
      <div style={contentStyle} onClick={(e) => e.stopPropagation()}>
        {loading ? (
          <div style={{ color: '#a6adc8', fontSize: '14px' }}>Loading preview...</div>
        ) : src ? (
          <img src={src} alt={`Preview of ${imagePath}`} style={imgStyle} />
        ) : (
          <div style={{ color: '#f38ba8', fontSize: '14px' }}>Failed to load image</div>
        )}
        <div style={metaStyle}>
          {imagePath}
          <br />
          Face #{faceIndex}
        </div>
      </div>
      <button style={closeBtn} onClick={onClose} title="Close">
        &times;
      </button>
    </div>
  );
}
